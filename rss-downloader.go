package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"html/template"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
)



type Item struct {
	Title       string `xml:"title"`
	Description string `xml:"description"`
	Content     string `xml:"encoded"`
	Link        string `xml:"link"`
	Author      string `xml:"author"`
	Guid        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
}

type Channel struct {
	Title       string  `xml:"title"`
	Link        string  `xml:"link"`
	PubDate     string  `xml:"lastBuildDate"`
	Description string  `xml:"description"`
	Items       []*Item `xml:"item"`
}

type Feed struct {
	Channel *Channel `xml:"channel"`
}



func (f *Feed) Parse(body []byte) error {
	err := xml.Unmarshal(body, &f)
	return err
}

func (f Feed) String() string {
	if f.Channel == nil {
		fmt.Println("nop")
		return ""
	}

	var body bytes.Buffer
	const tmpl = `{{.Title}}
{{.PubDate}}
<!DOCTYPE html>
<html>
	<head>
		<meta charset="UTF-8">
	</head>
	<body>
		<div><b>{{.Title}}</b></div>
		<hr>
		{{range .Items}}
		<div>
			<a href="{{.Link}}"><b>{{.Title}}</b> /{{.Date}}/</a><br>
			<div>
			{{.Text}}
			</div>
		</div>
		<hr>
		{{end}}
	</body>
</html>`

	type Rec struct {
		Title string
		Link  string
		Date  string
		Text  any
	}

	var recs []Rec

	data := struct {
		Title   string
		URL     string
		PubDate time.Time
		Items   []Rec
	}{
		Title: "",
		URL:   "",
		Items: recs,
	}

	data.Title = f.Channel.Title
	data.URL = f.Channel.Link
	data.PubDate = prepDate(f.Channel.PubDate)
	d_time := data.PubDate.String()

	if len(f.Channel.Items) == 0 {
		return ""
	}

	for _, item := range f.Channel.Items {
		data.Items = append(data.Items, Rec{Title: item.Title, Link: item.Link, Date: d_time, Text: template.HTML(item.Description)})
	}

	t, _ := template.New("webpage").Parse(tmpl)
	t.Execute(&body, data)
	return body.String()
}

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
	data_chan := make(chan Feed, rssCount)

	wg := new(sync.WaitGroup)

	for _, rss := range conf {
		wg.Add(1)
		go getData(wg, rss.Name, rss.URL, data_chan, rss.LastPubDate)
	}

	wg.Wait()

	for range rssCount {
		ss := <-data_chan
		if len(ss.String()) != 0 {
			tmp := strings.SplitN(ss.String(), "\n", 3)
			title := tmp[0]
			newdate := tmp[1]
			body := tmp[2]

			// заплатка: template возвращает HTML код, приходится переделывать
			r, _ := regexp.Compile("&#43;")
			newdate = string(r.ReplaceAll([]byte(newdate), []byte("+"))[:])
			if strings.HasPrefix(newdate, "0001") {
				newdate = time.Now().Format("2006-01-02 15:04:05 +0000 MST")
			}

			subject := title + " by RSS Downloader"
			err := sendMail(smtpCfg, email, subject, body)
			if err != nil {
				log.Printf("feed %s: send: %v", title, err)
				continue
			}

			fmt.Println("message was send")
			err = updateConfig(configFile, title, "lastPubDate", newdate)
			if err != nil {
				log.Printf("feed %s: update config: %v", title, err)
			}
		} else {
			fmt.Println("have not message for send")
		}
	}
}

func getData(wg *sync.WaitGroup, name string, url string, ch chan Feed, lastPubDate time.Time) {
	var (
		data Feed
		err  error
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	defer func() {
		cancel()
		if r := recover(); r != nil {
			fmt.Println("Recovered from:", r)
		}
		ch <- data
		wg.Done()
	}()

	feed, err := gofeed.NewParser().ParseURLWithContext(url, ctx)
	if err != nil {
		fmt.Printf("!%s, %s\n", err, url)
		return
	}

	data, err = parseFeed(feed, lastPubDate, name)
	if err != nil {
		fmt.Printf("!!%s", err)
		return
	}
}

func parseFeed(feed *gofeed.Feed, lastPubDate time.Time, sectionName string) (Feed, error) {
	var (
		f     = Feed{Channel: &Channel{Items: make([]*Item, 0)}}
		items []*Item
	)

	for _, item := range feed.Items {
		if item.PublishedParsed != nil && item.PublishedParsed.After(lastPubDate) {
			author := make([]string, 0)
			for _, a := range item.Authors {
				author = append(author, fmt.Sprintf("%s <%s>", a.Name, a.Email))
			}
			items = append(items, &Item{
				Title:       item.Title,
				Description: item.Description,
				Content:     item.Content,
				Link:        item.Link,
				Author:      strings.Join(author, ", "), // TODO
				PubDate:     item.PublishedParsed.GoString(),
			})
		}
	}

	f.Channel.Title = sectionName
	f.Channel.Items = items
	f.Channel.Link = feed.FeedLink

	return f, nil
}

func prepDate(d string) time.Time {
	loc, _ := time.LoadLocation("MSK")
	dd, d_err := time.ParseInLocation(time.RFC1123, d, loc)
	if d_err != nil {
		dd, _ = time.ParseInLocation(time.RFC1123Z, d, loc)
	}
	return dd
}



