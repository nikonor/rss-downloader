package main

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
)

func main() {
	configFile, err := defaultConfigPath()
	if err != nil {
		log.Fatalf("config path: %v", err)
	}
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	email, smtpCfg, links, err := readConfig(configFile)
	if err != nil {
		log.Fatalf("read config: %v", err)
	}

	ch := make(chan digest, len(links))

	wg := new(sync.WaitGroup)
	for _, l := range links {
		wg.Add(1)
		go fetchDigest(wg, l, ch)
	}

	wg.Wait()
	close(ch)

	for d := range ch {
		if d.body == "" {
			log.Printf("feed %s: no new items", d.name)
			continue
		}

		subject := d.name + " by RSS Downloader"
		if err := sendMail(smtpCfg, email, subject, d.body); err != nil {
			log.Printf("feed %s: send: %v", d.name, err)
			continue
		}

		log.Printf("feed %s: message sent", d.name)

		value := time.Now().Format(timeForm2)
		if err := updateConfig(configFile, d.name, "lastPubDate", value); err != nil {
			log.Printf("feed %s: update config: %v", d.name, err)
		}
	}
}

func fetchDigest(wg *sync.WaitGroup, l link, ch chan<- digest) {
	defer wg.Done()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	feed, err := gofeed.NewParser().ParseURLWithContext(l.URL, ctx)
	if err != nil {
		log.Printf("feed %s: fetch: %v", l.Name, err)
		return
	}

	ch <- digest{name: l.Name, body: renderDigest(l.Name, feed, l.LastPubDate)}
}
