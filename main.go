package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"syscall"

	"github.com/coreos/pkg/flagutil"
	"github.com/dghubble/go-twitter/twitter"
	"github.com/dghubble/oauth1"
	"github.com/julianshen/og"
	"github.com/shah/content-harvester-utils"
	"mvdan.cc/xurls"
)

type textList []string
type regExList []*regexp.Regexp

func (i *textList) String() string {
	return ""
}

func (i *textList) Set(value string) error {
	if value != "" {
		*i = append(*i, value)
	}
	return nil
}

func (i *regExList) String() string {
	return ""
}

func (i *regExList) Set(value string) error {
	if value != "" {
		*i = append(*i, regexp.MustCompile(value))
	}
	return nil
}

func main() {
	// TODO add ability to configure hooks or GraphQL subscriptions for outbound event calls
	var filterTrackItems textList
	var ignoreURLsRegEx regExList
	var removeParamsFromURLsRegEx regExList

	flags := flag.NewFlagSet("options", flag.ExitOnError)
	consumerKey := flags.String("consumer-key", "", "Twitter Consumer Key")
	consumerSecret := flags.String("consumer-secret", "", "Twitter Consumer Secret")
	accessToken := flags.String("access-token", "", "Twitter Access Token")
	accessSecret := flags.String("access-secret", "", "Twitter Access Secret")
	flags.Var(&filterTrackItems, "twitter-filter-track", "The items to track in Twitter Filter")
	flags.Var(&ignoreURLsRegEx, "ignore-urls-reg-ex", "Regular expression indicating which URL patterns to not harvest")
	flags.Var(&removeParamsFromURLsRegEx, "remove-params-from-urls-reg-ex", "Regular expression indicating which URL query params to 'clean' in harvested URLs")
	flags.Parse(os.Args[1:])
	flagutil.SetFlagsFromEnv(flags, "TWITTER")

	if *consumerKey == "" || *consumerSecret == "" || *accessToken == "" || *accessSecret == "" {
		log.Fatal("Consumer key/secret and Access token/secret required")
	}

	if len(filterTrackItems) == 0 {
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
	contentHarvester := harvester.MakeContentHarvester(xurls.Relaxed(), ignoreURLsRegEx, removeParamsFromURLsRegEx)

	demux := twitter.NewSwitchDemux()
	demux.Tweet = func(tweet *twitter.Tweet) {
		r := contentHarvester.HarvestResources(tweet.Text)
		for _, res := range r.Resources {
			_, isDestValid := res.IsValid()
			isIgnored, _ := res.IsIgnored()
			if isDestValid && !isIgnored {
				finalURL, _, _ := res.GetURLs()
				if tweet.Retweeted {
					fmt.Printf("[@%s <- @%s, %d]: %s (%s)\n", tweet.RetweetedStatus.User.ScreenName, tweet.User.ScreenName, tweet.User.FollowersCount, finalURL.String(), res.DestinationContentType())
				} else {
					fmt.Printf("[@%s, %d]: %s (%s)\n", tweet.User.ScreenName, tweet.User.FollowersCount, finalURL.String(), res.DestinationContentType())
				}

				// TODO move pageInfo as a method of Resource or Harvester, not in main
				pageInfo, err := og.GetPageInfoFromUrl(finalURL.String())
				if err == nil {
					//fmt.Printf("   Site: %s (%s)\n", pageInfo.SiteName, pageInfo.Site)
					fmt.Printf("  Title: %s\n", pageInfo.Title)
				}
			}
		}
	}

	fmt.Println("Starting Stream...")
	fmt.Println(filterTrackItems)
	filterParams := &twitter.StreamFilterParams{
		// TODO add command line option to read trackers from Lectio Campaign and other sources
		Track:         filterTrackItems,
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

	fmt.Println("Stopping Stream...")
	stream.Stop()
}
