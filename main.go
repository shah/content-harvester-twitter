package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"
	"time"

	"github.com/ChimeraCoder/anaconda"
	"github.com/coreos/pkg/flagutil"
	"github.com/ericaro/frontmatter"
	"github.com/peterbourgon/diskv"
	"github.com/shah/content-harvester-utils"
	"go.uber.org/zap"
)

// HarvestedResourceStorage is the database for harvested resources
type HarvestedResourceStorage struct {
	basePath         string
	diskv            *diskv.Diskv
	logger           *zap.Logger
	contentHarvester *harvester.ContentHarvester
}

// SaveAllInText all harvested resources into the database
func (storage *HarvestedResourceStorage) SaveAllInText(text string) {
	r := storage.contentHarvester.HarvestResources(text)
	for _, res := range r.Resources {
		isURLValid, isDestValid := res.IsValid()
		if !isURLValid {
			storage.logger.Info("Invalid URL", zap.String("source", text),
				zap.String("originalURLText", res.OriginalURLText()),
				zap.String("reason", "Not sure why"),
			)
			continue
		}
		if !isDestValid {
			isIgnored, ignoreReason := res.IsIgnored()
			if isIgnored {
				storage.logger.Info("Invalid URL Destination", zap.String("source", text),
					zap.String("originalURLText", res.OriginalURLText()),
					zap.String("reason", ignoreReason),
				)
			} else {
				storage.logger.Info("Invalid URL Destination", zap.String("source", text),
					zap.String("originalURLText", res.OriginalURLText()),
					zap.String("reason", "Unknown reason"),
				)
			}
			continue
		}
		finalURL, resolvedURL, cleanedURL := res.GetURLs()
		isIgnored, ignoreReason := res.IsIgnored()
		if isIgnored {
			storage.logger.Info("Ignored", zap.String("source", text),
				zap.String("originalURLText", res.OriginalURLText()),
				zap.String("reason", ignoreReason),
				zap.String("referredBy", resourceToString(res.ReferredByResource())),
				zap.String("finalURL", urlToString(finalURL)),
				zap.String("resolvedURL", urlToString(resolvedURL)),
			)
			continue
		}

		keys := harvester.CreateKeys(res)
		frontMatter := struct {
			Slug    string `yaml:"slug"`
			OrigURL string `yaml:"origURL"`
			Content string `fm:"content" yaml:"-"`
		}{
			Slug:    keys.Slug(),
			OrigURL: res.OriginalURLText(),
			Content: text,
		}

		data, err := frontmatter.Marshal(frontMatter)
		if err != nil {
			fmt.Printf("err! %s", err.Error())
		}
		storage.diskv.Write(keys.Slug(), data)

		storage.logger.Info("Saved", zap.String("source", text),
			zap.String("originalURLText", res.OriginalURLText()),
			zap.String("slug", keys.Slug()),
			zap.String("referredBy", resourceToString(res.ReferredByResource())),
			zap.String("finalURL", urlToString(finalURL)),
			zap.String("resolvedURL", urlToString(resolvedURL)),
			zap.String("cleanedURL", urlToString(cleanedURL)),
		)
	}
}

// NewHarvestedResourceStorage that can persist harvested resources
func NewHarvestedResourceStorage(contentHarvester *harvester.ContentHarvester, logger *zap.Logger, basePath string) *HarvestedResourceStorage {
	result := new(HarvestedResourceStorage)
	result.contentHarvester = contentHarvester
	result.logger = logger
	result.basePath = basePath

	// Simplest transform function: put all the data files into the base dir.
	flatTransform := func(s string) []string { return []string{} }

	// Initialize a new diskv store, rooted at "my-data-dir", with a 1MB cache.
	result.diskv = diskv.New(diskv.Options{
		BasePath:     basePath,
		Transform:    flatTransform,
		CacheSizeMax: 1024 * 1024,
	})

	return result
}

// *** MAJOR TODO ***
// The Streaming API is being retired in June:
//   https://blog.twitter.com/developer/en_us/topics/tools/2017/announcing-more-functionality-to-improve-customer-engagements-on-twitter.html

// TODO use http://websocketd.com/ to turn this into a streaming server
// TODO if using straight HTTP REST (and not GraphQL) consider https://github.com/gorilla/mux

// TODO use https://www.lukemorton.co.uk/thoughts/2017-01-15-deploying-go-on-zeit-now to figure
// how to run this on Zeit (like Node.js versions)

type textList []string
type ignoreURLsRegExList []*regexp.Regexp
type cleanURLsRegExList []*regexp.Regexp

var removeNewLinesRegEx = regexp.MustCompile(`\r?\n`)

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

func resourceToString(hr *harvester.HarvestedResource) string {
	if hr == nil {
		return ""
	}

	referrerURL, _, _ := hr.GetURLs()
	return urlToString(referrerURL)
}

func urlToString(url *url.URL) string {
	if url == nil {
		return ""
	}
	return url.String()
}

func createTweetTestData(contentHarvester *harvester.ContentHarvester, csvWriter *csv.Writer, tweet string) {
	time := time.Now().Format("01-02 15:04:05")
	r := contentHarvester.HarvestResources(tweet)
	tweetText := removeNewLinesRegEx.ReplaceAllString(tweet, " ")
	for _, res := range r.Resources {
		isURLValid, isDestValid := res.IsValid()
		if !isURLValid {
			csvWriter.Write([]string{time, tweetText, res.OriginalURLText(), "Invalid URL", "Not sure why"})
			continue
		}
		if !isDestValid {
			isIgnored, ignoreReason := res.IsIgnored()
			if isIgnored {
				csvWriter.Write([]string{time, tweetText, res.OriginalURLText(), "Invalid URL Destination", ignoreReason})
			} else {
				csvWriter.Write([]string{time, tweetText, res.OriginalURLText(), "Invalid URL Destination", "Unknown reason"})
			}
			continue
		}
		finalURL, resolvedURL, cleanedURL := res.GetURLs()
		isIgnored, ignoreReason := res.IsIgnored()
		if isIgnored {
			csvWriter.Write([]string{time, tweetText, res.OriginalURLText(), "Ignored", ignoreReason, resourceToString(res.ReferredByResource()), urlToString(finalURL), urlToString(resolvedURL)})
			continue
		}

		csvWriter.Write([]string{time, tweetText, res.OriginalURLText(), "Resolved", "Success", resourceToString(res.ReferredByResource()), urlToString(finalURL), urlToString(resolvedURL), urlToString(cleanedURL)})
	}
	csvWriter.Flush()
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
	storageBasePath := flags.String("storage-base-path", fmt.Sprintf("./tmp/storage-%s", time.Now().Format("2006-01-02-15-04-05")), "Name of the root directory to storage harvested resources in")
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

	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("can't initialize zap logger: %v", err)
	}
	defer logger.Sync()

	contentHarvester := harvester.MakeContentHarvester(ignoreURLsRegEx, removeParamsFromURLsRegEx, true)
	storage := NewHarvestedResourceStorage(contentHarvester, logger, *storageBasePath)
	twitterAPI := anaconda.NewTwitterApiWithCredentials(*accessToken, *accessSecret, *consumerKey, *consumerSecret)

	if *searchTwitter {
		fmt.Printf("Searching Twitter: %s in %s...\n", twitterQuery, *storageBasePath)
		searchResult, _ := twitterAPI.GetSearch(twitterQuery[0], nil)
		for _, tweet := range searchResult.Statuses {
			//createTweetTestData(contentHarvester, csvWriter, tweet.Text)
			storage.SaveAllInText(tweet.Text)
		}
		return
	}

	fmt.Printf("Starting Twitter Stream: %s in %s...\n", twitterQuery, *storageBasePath)
	v := url.Values{"track": twitterQuery}
	s := twitterAPI.PublicStreamFilter(v)

	for t := range s.C {
		switch v := t.(type) {
		case anaconda.Tweet:
			//createTweetTestData(contentHarvester, csvWriter, v.Text)
			storage.SaveAllInText(v.Text)
		}
	}
}
