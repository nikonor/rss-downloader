package main

import (
	"bytes"
	"html/template"
	"log"
	"time"

	"github.com/mmcdole/gofeed"
)

var (
	digestTemplate = template.Must(template.New("digest").Parse(digestTemplateText))
	msk            = loadMSK()
)

const digestTemplateText = `<!DOCTYPE html>
<html>
	<head>
		<meta charset="UTF-8">
	</head>
	<body>
		<div><b>{{.Name}}</b></div>
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

type digest struct {
	name string
	body string
}

type digestItem struct {
	Title string
	Link  string
	Date  string
	Text  template.HTML
}

func renderDigest(name string, feed *gofeed.Feed, lastPubDate time.Time) string {
	var items []digestItem
	for _, item := range feed.Items {
		if item.PublishedParsed == nil || !item.PublishedParsed.After(lastPubDate) {
			continue
		}

		items = append(items, digestItem{
			Title: item.Title,
			Link:  item.Link,
			Date:  item.PublishedParsed.In(msk).Format("2006-01-02 15:04"),
			Text:  template.HTML(item.Description),
		})
	}

	if len(items) == 0 {
		return ""
	}

	var body bytes.Buffer
	data := struct {
		Name  string
		Items []digestItem
	}{
		Name:  name,
		Items: items,
	}

	if err := digestTemplate.Execute(&body, data); err != nil {
		log.Printf("feed %s: render: %v", name, err)
		return ""
	}

	return body.String()
}

func loadMSK() *time.Location {
	loc, err := time.LoadLocation("MSK")
	if err != nil {
		return time.FixedZone("MSK", 3*60*60)
	}

	return loc
}
