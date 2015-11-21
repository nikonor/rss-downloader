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
	"github.com/robfig/config"
	"html/template"
	"bytes"
)

const timeForm = "2006-01-02 15:04:05 +0000 MST"

type link struct {
	Name string
	URL  string
	LastPubDate time.Time
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
	var body bytes.Buffer
	const tmpl = `
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
			<a href="{{.Link}}">{{.Title}} /{{.Date}}/</a><br>
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
		Link string
		Date string
		Text interface{}
	}

	var recs []Rec

	data := struct {
		Title string
		URL string
		Items []Rec
	}{
		Title:"",
		URL:"",
		Items:recs,
	}


	data.Title = f.Channel.Title
	for _, item := range f.Channel.Items {
		data.Items = append(data.Items,Rec{	Title:item.Title, Link:item.Link, Date: item.PubDate, Text: template.HTML(item.Description) })
	}
	
	t,_ := template.New("webpage").Parse(tmpl)
	t.Execute(&body, data)
	return body.String()
}

var (
	conf      []link
	rss_count int
	rsses     []rss
	email	string
)

type rss struct {
	Data map[string]string
}

func init() {

	email,conf = readConfig("")
	rss_count = len(conf)

}

func main() {
	fmt.Println("Start main")

	data_chan := make(chan Feed, rss_count)

	for _, rss := range conf {
		fmt.Printf("%s=%v\n",rss.Name,rss.LastPubDate);
		go get_data(rss.Name, rss.URL, data_chan, rss.LastPubDate)
	}

	for i := 0; i < int(rss_count); i++ {
		ss := <-data_chan
		fmt.Println(ss)
	}
	fmt.Println("Finish main")
}

func get_data(name string, url string, ch chan Feed, lastPubDate time.Time) {
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
	ch <- parse_rss(contents, lastPubDate)
}


func parse_rss(rss []byte, lastPubDate time.Time) Feed {
	f := Feed{}
	var f_items []*Item

	err := f.Parse(rss)

	for _, item := range f.Channel.Items {
		item_date,d_err := time.Parse(time.RFC1123Z,item.PubDate)
		if d_err != nil {
			item_date,_ = time.Parse(time.RFC1123,item.PubDate)
		}

		if item_date.After(lastPubDate) {
			f_items = append(f_items,item)
		}
	}

	if err != nil {
		log.Fatal(err)
	}

	f.Channel.Items = f_items

	return f
}

func readConfig(filename string) (string, []link){
	var conf []link
	var email string
	if filename == "" {
		usr, _ := user.Current()
		filename = strings.Join([]string{usr.HomeDir,".rss-downloader.conf"},"/")
	}

	cfg, err := config.ReadDefault(filename)
	if err != nil {
		panic("Error on read config file")
	}
	sections := cfg.Sections()
	for i := range sections {

		if sections[i] == "DEFAULT" {
			email, _ = cfg.String(sections[i], "email")
		} else {
			fmt.Printf("!%s!\n", sections[i])
			url, _ := cfg.String(sections[i], "url")
			t_string, _ := cfg.String(sections[i], "lastPubDate")
			t_time,_ := time.Parse(timeForm,t_string)
			conf = append(conf, link{sections[i],url,t_time})
		}	
	}
	return email,conf
}
