package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/xml"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/smtp"
	"os"
	"os/user"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/robfig/config"
)

const (
	timeForm  = "2006-01-02 15:04:05 +0000 MST"
	timeForm2 = "2006-01-02 15:04:05 +0000 +0000"
)

type link struct {
	Name        string
	URL         string
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
	Title       string  `xml:"title"`
	Link        string  `xml:"link"`
	PubDate     string  `xml:"lastBuildDate"`
	Description string  `xml:"description"`
	Items       []*Item `xml:"item"`
}

type Feed struct {
	Channel *Channel `xml:"channel"`
}

type smtp_conn_type struct {
	Login    string
	Password string
	Host     string
	Port     string
}

type rss struct {
	Data map[string]string
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
	conf      []link
	rssCount  int
	rsses     []rss
	email     string
	smtp_conn smtp_conn_type
)

func init() {
	configfile := ""
	if len(os.Args) > 1 {
		configfile = os.Args[1]
	}
	email, smtp_conn, conf = readConfig(configfile)
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

			mime := "MIME-version: 1.0;\nContent-Type: text/html; charset=\"UTF-8\";\n\n"
			header := "From: RSS Downloader<rss-dl@nikonor.ru>\nTo: " + email + "\nSubject: " + title + " by RSS Downloader\n"
			msg := []byte(header + mime + body)
			err := sendDigest(smtp_conn, msg)
			if err != nil {
				log.Fatal(err)
			} else {
				fmt.Println("message was send", newdate)
				err := updateConfig("", title, "lastPubDate", newdate)
				if err != nil {
					log.Fatal(err)
				}

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

func updateConfig(filename string, section string, key string, value string) error {
	if filename == "" {
		usr, _ := user.Current()
		filename = strings.Join([]string{usr.HomeDir, ".rss-downloader.conf"}, "/")
		if len(os.Args) > 1 {
			filename = os.Args[1]
		}
	}

	cfg, err := config.ReadDefault(filename)
	if err != nil {
		panic("Error on read config file")
	}

	fmt.Println(section, key, value)
	cfg.AddOption(section, key, value)

	cfg.WriteFile(filename, 0644, "rss downloader config file")
	if err != nil {
		return err
	}

	return nil
}

func readConfig(filename string) (string, smtp_conn_type, []link) {
	var (
		conf   []link
		email  string
		s_conn smtp_conn_type
	)

	if filename == "" {
		usr, _ := user.Current()
		filename = strings.Join([]string{usr.HomeDir, ".rss-downloader.conf"}, "/")
	}

	cfg, err := config.ReadDefault(filename)
	if err != nil {
		panic("Error on read config file")
	}
	sections := cfg.Sections()
	for i := range sections {
		if sections[i] == "DEFAULT" {
			email, _ = cfg.String(sections[i], "email")
			s_conn.Login, _ = cfg.String(sections[i], "smtp_login")
			s_conn.Password, _ = cfg.String(sections[i], "smtp_passwd")
			srv_str, _ := cfg.String(sections[i], "smtp_server")
			s_conn.Host, s_conn.Port, _ = net.SplitHostPort(srv_str)
		} else {
			url, _ := cfg.String(sections[i], "url")
			t_string, _ := cfg.String(sections[i], "lastPubDate")
			t_time, t_err := time.Parse(timeForm, t_string)
			if t_err != nil {
				t_time, t_err = time.Parse(timeForm2, t_string)
			}
			conf = append(conf, link{sections[i], url, t_time})
		}
	}

	return email, s_conn, conf
}

// copy & past https://gist.github.com/chrisgillis/10888032
func sendDigest(smtp_conn smtp_conn_type, msg []byte) error {
	servername := strings.Join([]string{smtp_conn.Host, smtp_conn.Port}, ":")

	auth := smtp.PlainAuth("", smtp_conn.Login, smtp_conn.Password, smtp_conn.Host)

	// TLS config
	tlsconfig := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         smtp_conn.Host,
	}

	conn, err := tls.Dial("tcp", ""+servername, tlsconfig)
	if err != nil {
		return err
	}

	c, err := smtp.NewClient(conn, smtp_conn.Host)
	if err != nil {
		return err
	}

	if err = c.Auth(auth); err != nil {
		return err
	}

	if err = c.Mail(smtp_conn.Login); err != nil {
		return err
	}

	if err = c.Rcpt(email); err != nil {
		return err
	}

	w, err := c.Data()
	if err != nil {
		return err
	}

	_, err = w.Write(msg)
	if err != nil {
		return err
	}

	err = w.Close()
	if err != nil {
		return err
	}

	c.Quit()

	return nil
}
