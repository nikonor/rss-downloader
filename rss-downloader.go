package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
)









var (
	configFile string
	conf       []link
	rssCount   int
	email      string
	smtpCfg    smtpConn
)

func init() {
	var err error
	configFile, err = defaultConfigPath()
	if err != nil {
		log.Fatal(err)
	}
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	email, smtpCfg, conf, err = readConfig(configFile)
	if err != nil {
		log.Fatalf("read config: %v", err)
	}

	rssCount = len(conf)
}

func main() {
	ch := make(chan digest, rssCount)

	wg := new(sync.WaitGroup)
	for _, l := range conf {
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
		err := sendMail(smtpCfg, email, subject, d.body)
		if err != nil {
			log.Printf("feed %s: send: %v", d.name, err)
			continue
		}

		fmt.Println("message was send")
		err = updateConfig(configFile, d.name, "lastPubDate", time.Now().Format(timeForm2))
		if err != nil {
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



