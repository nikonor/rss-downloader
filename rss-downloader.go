package main

import (
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/user"
	"strings"
	"time"
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

type smtp_conn_type struct{
	Login string
	Password string
	Host string
	Port string
}

type rss struct {
	Data map[string]string
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
	smtp_conn smtp_conn_type
)


func init() {
	email, smtp_conn, conf = readConfig("")
	rss_count = len(conf)
}

func main() {
	fmt.Println("Start main")

	data_chan := make(chan Feed, rss_count)

	for _, rss := range conf {
		go get_data(rss.Name, rss.URL, data_chan, rss.LastPubDate)
	}

	for i := 0; i < int(rss_count); i++ {
		ss := <-data_chan
		mime := "MIME-version: 1.0;\nContent-Type: text/html; charset=\"UTF-8\";\n\n";
		header := "From: RSS Downloader<rss-dl@nikonor.ru>\nTo: "+email+"\nSubject: RSS Downloader digest!\n"
		msg := []byte(header + mime + ss.String())		
		// fmt.Printf("===========\n%s\n===========\n",msg)
		err := send_digest(smtp_conn,msg)
		if err != nil {
			log.Fatal(err)
		} else {
			fmt.Println("\tmail was sending");
		}
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

func send_digest (conn smtp_conn_type, msg []byte) (error) {
	return nil
}

func readConfig(filename string) (string, smtp_conn_type, []link){
	var (
		conf []link
		email string
		s_conn smtp_conn_type
	)

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
			s_conn.Login,_ = cfg.String(sections[i], "smtp_login")
			s_conn.Password,_ = cfg.String(sections[i], "smtp_passwd")
			srv_str,_ := cfg.String(sections[i], "smtp_server")
			s_conn.Host, s_conn.Port, _ = net.SplitHostPort(srv_str)
		} else {
			url, _ := cfg.String(sections[i], "url")
			t_string, _ := cfg.String(sections[i], "lastPubDate")
			t_time,_ := time.Parse(timeForm,t_string)
			conf = append(conf, link{sections[i],url,t_time})
		}	
	}

	return email,s_conn,conf
}
