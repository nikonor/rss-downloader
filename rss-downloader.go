package main

import (
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
	"os/user"
)

type link struct {
	Name string
	URL  string
}

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
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	PubDate     string 	  `xml:"lastBuildDate"`
	Description string    `xml:"description"`
	Items       []*Item   `xml:"item"`
}

type Feed struct {
	Channel *Channel `xml:"channel"`
}

func (f *Feed) Parse(body []byte) error {
	err := xml.Unmarshal(body, &f)
	return err
}

func (f Feed) String() string {
	var res string
	channel_date,_ := time.Parse(time.RFC1123Z,f.Channel.PubDate)
	res += fmt.Sprintf("Feed: %s (%s) /%v/\n", f.Channel.Title,f.Channel.Link,channel_date)
	for _, item := range f.Channel.Items {
		d,_ := time.Parse(time.RFC1123Z,item.PubDate)
		res += fmt.Sprintf("\t%s\n", item.Link)
		res += fmt.Sprintf("\t%s\n", item.Title)
		res += fmt.Sprintf("\t%s=%v!\n", item.PubDate,d)
		res += fmt.Sprintf("\t%s\n\n", item.Description)
	}
	return res
}

var (
	conf      []link
	rss_count int64
	rsses     []rss
)

type rss struct {
	Data map[string]string
}

func init() {
	usr, _ := user.Current()
	conf_file, err := os.Open(strings.Join([]string{usr.HomeDir,".rss-downloader.conf"},"/"))
	defer conf_file.Close()
	if err != nil {
		log.Fatal(err)
	}

	fi, err := conf_file.Stat()
	if err != nil {
		log.Fatal(err)
	}

	conf_body_b := make([]byte, fi.Size())

	size, err := conf_file.Read(conf_body_b)
	if err != nil {
		log.Fatal(err)
	} else if int64(size) != fi.Size() {
		log.Fatal("Ошибка чтения файла")
	}
	conf_body_s := string(conf_body_b[:size])

	for _, s := range strings.Split(conf_body_s, "\n") {
		ss := strings.SplitN(s, ";", 3)
		if len(ss) == 3 {
			conf = append(conf, link{ss[1], ss[2]})
			rss_count++
		}
	}
}

func main() {
	fmt.Println("Start main")

	data_chan := make(chan Feed, rss_count)

	for _, rss := range conf {
		go get_data(rss.Name, rss.URL, data_chan)
	}

	for i := 0; i < int(rss_count); i++ {
		ss := <-data_chan
		fmt.Println(ss)
	}
	fmt.Println("Finish main")
}

func get_data(name string, url string, ch chan Feed) {
	response, err := http.Get(url)

	if err != nil {
		fmt.Printf("%s", err)
		os.Exit(1)
	}
	defer response.Body.Close()
	contents, err := ioutil.ReadAll(response.Body)
	if err != nil {
		fmt.Printf("%s", err)
		os.Exit(1)
	}
	ch <- parse_rss(contents)
}


func parse_rss(rss []byte) Feed {
	f := Feed{}
	err := f.Parse(rss)
	if err != nil {
		log.Fatal(err)
	}

	return f
}
