package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/coreos/pkg/flagutil"
	"github.com/dghubble/go-twitter/twitter"
	"github.com/dghubble/oauth1"
	"github.com/julianshen/og"
	"github.com/shah/content-harvester-utils"
)

func main() {
	// TODO add ability to configure default Regex's for harvester through here too

	flags := flag.NewFlagSet("user-auth", flag.ExitOnError)
	consumerKey := flags.String("consumer-key", "", "Twitter Consumer Key")
	consumerSecret := flags.String("consumer-secret", "", "Twitter Consumer Secret")
	accessToken := flags.String("access-token", "", "Twitter Access Token")
	accessSecret := flags.String("access-secret", "", "Twitter Access Secret")
	flags.Parse(os.Args[1:])
	flagutil.SetFlagsFromEnv(flags, "TWITTER")

	if *consumerKey == "" || *consumerSecret == "" || *accessToken == "" || *accessSecret == "" {
		log.Fatal("Consumer key/secret and Access token/secret required")
	}

	config := oauth1.NewConfig(*consumerKey, *consumerSecret)
	token := oauth1.NewToken(*accessToken, *accessSecret)
	// OAuth1 http.Client will automatically authorize Requests
	httpClient := config.Client(oauth1.NoContext, token)

	client := twitter.NewClient(httpClient)
	contentHarvester := harvester.MakeDefaultContentHarvester()

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
				pageInfo, err := og.GetPageInfoFromUrl(finalURL.String())
				if err == nil {
					//fmt.Printf("   Site: %s (%s)\n", pageInfo.SiteName, pageInfo.Site)
					fmt.Printf("  Title: %s\n", pageInfo.Title)
				}
			}
		}
	}

	fmt.Println("Starting Stream...")
	filterParams := &twitter.StreamFilterParams{
		// TODO read the trackers from command line
		// TODO add command line option to read trackers from Lectio Campaign and other sources
		Track:         []string{"trump"},
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
