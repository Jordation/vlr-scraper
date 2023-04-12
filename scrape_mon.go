package main

import (
	"fmt"
	"strings"

	stat_db "github.com/Jordation/go-api/server/db"
	"github.com/gocolly/colly/v2"
	log "github.com/sirupsen/logrus"
)

// a vlr match link could look like this https://www.vlr.gg/183774
// the start is the number which specifies which page to load, the count is how many to check in front for valid pages.
// it goes over the count because it's easier to check 10 at a time

// url to scrape chan	->	Scrape()
// MatchDataset chan	->	CreateDbEntries()
// done
func makeUrl(num interface{}) string {
	return fmt.Sprintf("https://www.vlr.gg/%v", num)
}
func makeMatchesUrl(event string) string {
	return fmt.Sprintf("https://www.vlr.gg/event/matches/%v/?series_id=all", event)
}
func filterUrls(urls []string) []string {
	res := make([]string, 0)
	for _, v := range urls {
		var l stat_db.HitLink
		result := Db.Where("link = ?", v).Take(&l)
		if result.Error != nil {
			res = append(res, v)
		}
	}
	return res
}

func GetCollector() *colly.Collector {
	c := colly.NewCollector(
		colly.AllowedDomains("www.vlr.gg"),
		colly.Async(true),
		colly.MaxDepth(3),
	)
	c.Limit(&colly.LimitRule{Parallelism: 2})

	return c
}
func GetVCTmatches(c *colly.Collector) []string {
	// the initial page to visit which contains event links
	start := "https://www.vlr.gg/vct-2021/?region=3"
	max_events := 15

	//handler for taking match URL's from events page
	matches := make([]string, 0)
	c.OnHTML("a.match-item[href]", func(h *colly.HTMLElement) {
		match := strings.Split(h.Attr("href"), "/")
		if len(match) > 1 {
			// looks like /matchID/other stuff
			matches = append(matches, makeUrl(match[1]))
		}
	})

	// handler for finding the events match pages
	c.OnHTML("a.event-item[href]", func(h *colly.HTMLElement) {
		// i think theres a memory leak lol.
		if h.Index > max_events {
			return
		}
		if strings.HasPrefix(h.Attr("href"), "/event/") {
			event := strings.Split(h.Attr("href"), "/")
			eventID := event[2]
			eventMatches := makeMatchesUrl(eventID)
			h.Request.Visit(eventMatches)
		}

	})
	c.Visit(start)
	c.Wait()
	return matches
}

func GetCleanDataChan(urls []string, c *colly.Collector) <-chan MatchDataset {
	dataChan := make(chan MatchDataset)
	go func() {
		for _, url := range urls {
			data, err := Scrape(url, c)
			if err != nil {
				log.Info(err)
				continue
			}
			log.Info("Scraped ", url)
			log.Info("data sample ", data.Shared.Event)
			dataChan <- data
		}
		close(dataChan)
	}()
	return dataChan
}

func InsertData(data <-chan MatchDataset) {
	go func(data <-chan MatchDataset) {
		for dataset := range data {
			if err := CreateDbEntries(dataset, Db); err != nil {
				log.Info(err)
			}
		}
	}(data)
}
