package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/coreos/pkg/flagutil"
	"github.com/dghubble/go-twitter/twitter"
	"github.com/dghubble/oauth1"
	"github.com/julianshen/og"
	"github.com/shah/content-harvester-utils"
)

// TODO use https://www.lukemorton.co.uk/thoughts/2017-01-15-deploying-go-on-zeit-now to figure
// how to run this on Zeit (like Node.js versions)

type textList []string
type ignoreURLsRegExList []*regexp.Regexp
type cleanURLsRegExList []*regexp.Regexp

func (i *textList) String() string {
	return ""
}

func (i *textList) Set(value string) error {
	if value != "" {
		*i = append(*i, value)
	}
	return nil
}

func (l *ignoreURLsRegExList) String() string {
	return ""
}

func (l *ignoreURLsRegExList) Set(value string) error {
	if value != "" {
		*l = append(*l, regexp.MustCompile(value))
	}
	return nil
}

func (l ignoreURLsRegExList) IgnoreDiscoveredResource(url *url.URL) (bool, string) {
	URLtext := url.String()
	for _, regEx := range l {
		if regEx.MatchString(URLtext) {
			return true, fmt.Sprintf("Matched Ignore Rule `%s`", regEx.String())
		}
	}
	return false, ""
}

func (l *cleanURLsRegExList) String() string {
	return ""
}

func (l *cleanURLsRegExList) Set(value string) error {
	if value != "" {
		*l = append(*l, regexp.MustCompile(value))
	}
	return nil
}

func (l cleanURLsRegExList) CleanDiscoveredResource(url *url.URL) bool {
	// we try to clean all URLs, not specific ones
	return true
}

func (l cleanURLsRegExList) RemoveQueryParamFromResource(paramName string) (bool, string) {
	for _, regEx := range l {
		if regEx.MatchString(paramName) {
			return true, fmt.Sprintf("Matched cleaner rule `%s`", regEx.String())
		}
	}

	return false, ""
}

func handleTweet(contentHarvester *harvester.ContentHarvester, tweet *twitter.Tweet) {
	time := time.Now()
	fmt.Printf("\r[%s] %s\r", time.Format("01-02 15:04:05"), tweet.Text)
	r := contentHarvester.HarvestResources(tweet.Text)
	for _, res := range r.Resources {
		_, isDestValid := res.IsValid()
		isIgnored, _ := res.IsIgnored()
		if isDestValid && !isIgnored {
			finalURL, _, _ := res.GetURLs()
			if tweet.Retweeted {
				fmt.Printf("\n[%s] {@%s <- @%s, %d}: %s (%s): ", time.Format("01-02 15:04:05"), tweet.RetweetedStatus.User.ScreenName, tweet.User.ScreenName, tweet.User.FollowersCount, finalURL.String(), res.DestinationContentType())
			} else {
				fmt.Printf("\n[%s] {@%s, %d}: %s (%s): ", time.Format("01-02 15:04:05"), tweet.User.ScreenName, tweet.User.FollowersCount, finalURL.String(), res.DestinationContentType())
			}

			// TODO move pageInfo as a method of Resource or Harvester, not in main
			pageInfo, err := og.GetPageInfoFromUrl(finalURL.String())
			if err == nil {
				//fmt.Printf("   Site: %s (%s)\n", pageInfo.SiteName, pageInfo.Site)
				fmt.Printf("%s\n", pageInfo.Title)
			} else {
				fmt.Printf("\n")
			}
		}
	}
}

func main() {
	// TODO add ability to configure hooks or GraphQL subscriptions for outbound event calls
	var twitterQuery textList
	var ignoreURLsRegEx ignoreURLsRegExList
	var removeParamsFromURLsRegEx cleanURLsRegExList

	// I've created this Twitter App: https://apps.twitter.com/app/15163306
	flags := flag.NewFlagSet("options", flag.ExitOnError)
	consumerKey := flags.String("consumer-key", "", "Twitter Consumer Key")
	consumerSecret := flags.String("consumer-secret", "", "Twitter Consumer Secret")
	accessToken := flags.String("access-token", "", "Twitter Access Token")
	accessSecret := flags.String("access-secret", "", "Twitter Access Secret")
	filterTwitterStream := flags.Bool("filter-stream", false, "Search for content in a continuous Twitter filter (until Ctrl+C is pressed)")
	searchTwitter := flags.Bool("search", false, "Search for content in Twitter and return results")
	flags.Var(&twitterQuery, "query", "The items to search in Twitter Filter")
	flags.Var(&ignoreURLsRegEx, "ignore-urls-reg-ex", "Regular expression indicating which URL patterns to not harvest")
	flags.Var(&removeParamsFromURLsRegEx, "remove-params-from-urls-reg-ex", "Regular expression indicating which URL query params to 'clean' in harvested URLs")
	flags.Parse(os.Args[1:])
	flagutil.SetFlagsFromEnv(flags, "TWITTER")

	if !*filterTwitterStream && !*searchTwitter {
		log.Fatal("Either filter-stream or search should be specified")
	}

	if *consumerKey == "" || *consumerSecret == "" || *accessToken == "" || *accessSecret == "" {
		log.Fatal("Consumer key/secret and Access token/secret required")
	}

	if len(twitterQuery) == 0 {
		log.Fatal("Twitter filter track items required")
	}

	if len(ignoreURLsRegEx) == 0 {
		ignoreURLsRegEx = []*regexp.Regexp{regexp.MustCompile(`^https://twitter.com/(.*?)/status/(.*)$`), regexp.MustCompile(`https://t.co`)}
	}

	if len(removeParamsFromURLsRegEx) == 0 {
		removeParamsFromURLsRegEx = []*regexp.Regexp{regexp.MustCompile(`^utm_`)}
	}

	config := oauth1.NewConfig(*consumerKey, *consumerSecret)
	token := oauth1.NewToken(*accessToken, *accessSecret)
	// OAuth1 http.Client will automatically authorize Requests
	httpClient := config.Client(oauth1.NoContext, token)

	client := twitter.NewClient(httpClient)
	contentHarvester := harvester.MakeContentHarvester(ignoreURLsRegEx, removeParamsFromURLsRegEx)

	if *searchTwitter {
		fmt.Println("Searching...")
		fmt.Println(twitterQuery)
		search, _, err := client.Search.Tweets(&twitter.SearchTweetParams{
			Query: twitterQuery[0],
		})
		if err != nil {
			log.Fatal(err)
		}
		for _, tweet := range search.Statuses {
			handleTweet(contentHarvester, &tweet)
		}
		return
	}

	demux := twitter.NewSwitchDemux()
	demux.Tweet = func(tweet *twitter.Tweet) {
		handleTweet(contentHarvester, tweet)
	}

	// TODO it seems that the stream "dies" after a few hours. I'm not sure if this requires some sort of
	// auto-restart or another fix but without a fix this utility cannot run as a continuous daemon.
	fmt.Println("Starting Twitter Stream...")
	fmt.Println(twitterQuery)
	filterParams := &twitter.StreamFilterParams{
		// TODO add command line option to read trackers from Lectio Campaign and other sources
		Track:         twitterQuery,
		StallWarnings: twitter.Bool(true),
	}
	stream, err := client.Streams.Filter(filterParams)
	if err != nil {
		log.Fatal(err)
	}

	// Receive messages until stopped or stream quits
	go demux.HandleChan(stream.Messages)

	// Wait for SIGINT and SIGTERM (HIT CTRL-C)
	ch := make(chan os.Signal)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	log.Println(<-ch)

	fmt.Println("Stopping Twitter Stream...")
	stream.Stop()
}
