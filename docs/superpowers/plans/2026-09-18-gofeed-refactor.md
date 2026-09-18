# Рефакторинг rss-downloader на прямое использование gofeed — Implementation Plan

**Goal:** Убрать собственные структуры фида, работать типами `gofeed` напрямую, удалить мёртвый код и исправить ханги/падения (нетаймаутенный SMTP, `log.Fatal` на ошибке отправки, глотающий `recover()`), разложив один файл `rss-downloader.go` на четыре файла по ответственности.

**Architecture:** Инкрементальный рефакторинг: на каждом шаге пакет `main` остаётся компилируемым и прогоняется `go build ./... && go vet ./...`. Сначала выносится `config.go`, затем `mailer.go`, затем `digest.go` (чистый рендер из `*gofeed.Feed`), в конце — финальный `main.go` без глобальных переменных и `rss-downloader.go` удаляется. Вместо передачи сырого фида по каналу передаётся готовый `digest{name, body}` — хак с разбором отрендеренного HTML по `\n` исчезает.

**Tech Stack:** Go 1.26.5 (vendor), `github.com/mmcdole/gofeed v1.4.2`, `github.com/robfig/config`, stdlib (`net/smtp`, `crypto/tls`, `html/template`, `log`).

## Global Constraints

- Тесты **не пишутся** (решение спецификации). Проверка каждого шага — `go build ./...` и `go vet ./...`; финальная проверка — ручные прогоны (Task 5).
- Новых зависимостей **не добавлять**; `go.mod` не менять.
- Логирование — stdlib `log` (`log.Printf` / `log.Fatalf`). `isp-kit` в проекте не используется, это standalone CLI.
- Ошибки оборачивать через `fmt.Errorf("...: %w", err)` (пакета isp-kit/errors в проекте нет).
- Таймауты: fetch фида — `5 * time.Second`; dial SMTP — `15 * time.Second`; дедлайн SMTP-сессии — `30 * time.Second`.
- Exit code: `0` во всех нештатных ситуациях, кроме нечитаемого конфига (`log.Fatalf`, код 1).
- Формат INI-конфига, формат писем (`From`, `Subject`, MIME-заголовок) и общий сценарий — **не меняются**.
- TLS-конфиг SMTP остаётся `InsecureSkipVerify: true`, порт 465, `PlainAuth` — без изменений.
- Идентификаторы — `MixedCaps`/`mixedCaps`, без подчёркиваний; комментарии в коде не добавлять.
- Финальный набор файлов пакета: `main.go`, `config.go`, `digest.go`, `mailer.go`.

**API `robfig/config` (проверено по vendor):**
- `config.ReadDefault(fname string) (*Config, error)`
- `cfg.Sections() []string`
- `cfg.String(section, option string) (string, error)`
- `cfg.AddOption(section, option, value string) bool`
- `cfg.WriteFile(fname string, perm os.FileMode, header string) error` ← возвращает ошибку (текущий код проверяет **чужую** переменную `err` — это баг, который исправляет Task 1)

---

### Task 1: `config.go` — чтение и запись конфига

**Files:**
- Create: `config.go`
- Modify: `rss-downloader.go` — удалить `const (timeForm, timeForm2)`, типы `link` и `smtp_conn_type`, структуру `rss`, переменную `rsses`, функции `readConfig` и `updateConfig` (старые версии); переписать `init()`; в `main()` заменить вызов `updateConfig("", ...)` на `updateConfig(configFile, ...)`

**Interfaces:**
- Consumes: `robfig/config` (vendor)
- Produces:
  - `type link struct { Name string; URL string; LastPubDate time.Time }`
  - `type smtpConn struct { Login, Password, Host, Port string }`
  - `defaultConfigPath() (string, error)` — `~/.rss-downloader.conf`
  - `readConfig(filename string) (string, smtpConn, []link, error)` — возврат: email, smtp, ссылок, error
  - `updateConfig(filename, section, key, value string) error`
  - константы `timeForm = "2006-01-02 15:04:05 +0000 MST"`, `timeForm2 = "2006-01-02 15:04:05 +0000 +0000"`

- [ ] **Step 1: Создать `config.go`**

```go
package main

import (
	"fmt"
	"net"
	"os/user"
	"strings"
	"time"

	"github.com/robfig/config"
)

const (
	timeForm  = "2006-01-02 15:04:05 +0000 MST"
	timeForm2 = "2006-01-02 15:04:05 +0000 +0000"
)

type (
	link struct {
		Name        string
		URL         string
		LastPubDate time.Time
	}

	smtpConn struct {
		Login    string
		Password string
		Host     string
		Port     string
	}
)

func defaultConfigPath() (string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("get current user: %w", err)
	}

	return strings.Join([]string{usr.HomeDir, ".rss-downloader.conf"}, "/"), nil
}

func readConfig(filename string) (string, smtpConn, []link, error) {
	cfg, err := config.ReadDefault(filename)
	if err != nil {
		return "", smtpConn{}, nil, fmt.Errorf("read config %s: %w", filename, err)
	}

	var (
		email string
		conn  smtpConn
		links []link
	)

	sections := cfg.Sections()
	for i := range sections {
		if sections[i] == "DEFAULT" {
			email, _ = cfg.String(sections[i], "email")
			conn.Login, _ = cfg.String(sections[i], "smtp_login")
			conn.Password, _ = cfg.String(sections[i], "smtp_passwd")
			server, _ := cfg.String(sections[i], "smtp_server")
			host, port, err := net.SplitHostPort(server)
			if err != nil {
				conn.Host = server
				conn.Port = "465"
			} else {
				conn.Host = host
				conn.Port = port
			}
		} else {
			url, _ := cfg.String(sections[i], "url")
			date, _ := cfg.String(sections[i], "lastPubDate")
			links = append(links, link{Name: sections[i], URL: url, LastPubDate: parseLastPubDate(date)})
		}
	}

	return email, conn, links, nil
}

func parseLastPubDate(s string) time.Time {
	for _, form := range []string{timeForm, timeForm2} {
		t, err := time.Parse(form, s)
		if err == nil {
			return t
		}
	}

	return time.Time{}
}

func updateConfig(filename, section, key, value string) error {
	cfg, err := config.ReadDefault(filename)
	if err != nil {
		return fmt.Errorf("read config %s: %w", filename, err)
	}

	cfg.AddOption(section, key, value)

	if err := cfg.WriteFile(filename, 0644, "rss downloader config file"); err != nil {
		return fmt.Errorf("write config %s: %w", filename, err)
	}

	return nil
}
```

Поведение: опции внутри секций читаются с игнорированием ошибки (как сейчас — отсутствующая опция даёт пустую строку); файл без `lastPubDate` или с нераспознаваемой датой → нулевое время, фид считается новым целиком; `smtp_server` без порта → порт `465`; ошибка чтения/записи файла возвращается вызывающему.

- [ ] **Step 2: Удалить из `rss-downloader.go` старую конфигурацию**

Удалить:
- блок `const (timeForm ... timeForm2 ...)`;
- `type link struct { ... }`;
- `type rss struct { ... }` (мёртвая структура);
- `type smtp_conn_type struct { ... }`;
- функцию `func updateConfig(filename string, section string, key string, value string) error` (старую, с `panic` и непроверяемой `WriteFile`);
- функцию `func readConfig(filename string) (string, smtp_conn_type, []link)` (старую, с `panic`).

- [ ] **Step 3: Переписать глобальные переменные и `init()`**

Блок `var (conf, rssCount, rsses, email, smtp_conn)` и `func init()` заменить на:

```go
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
```

Вместо прежнего `panic("Error on read config file")` теперь — `log.Fatalf` с текстом ошибки. Глобальная переменная намеренно называется `smtpCfg`: `smtpConn` занято именем типа, а `smtp` затеняет пакет `net/smtp`, пока `sendDigest` ещё живёт в `rss-downloader.go` (удаляется в Task 2).

- [ ] **Step 4: Обновить вызов `updateConfig` в `main()`**

В `main()` строку

```go
			err := updateConfig("", title, "lastPubDate", newdate)
```

заменить на

```go
			err := updateConfig(configFile, title, "lastPubDate", newdate)
```

(разрешение пути к файлу теперь происходит один раз, в `init()`, и путь передаётся явно).

- [ ] **Step 5: Проверить сборку**

Run: `go build ./... && go vet ./...`
Expected: без ошибок. Go сам сообщит о неиспользуемых импортах/неопределённых именах в `rss-downloader.go` — исправить (удалить неиспользуемые импорты), повторить шаг.

- [ ] **Step 6: Коммит**

Run: `git add -A && git commit -m "config: move to config.go, return errors instead of panic"`

---

### Task 2: `mailer.go` — отправка SMTP с таймаутами

**Files:**
- Create: `mailer.go`
- Modify: `rss-downloader.go` — удалить `sendDigest`; в `main()` заменить блок отправки письма

**Interfaces:**
- Consumes: `smtpConn` из Task 1
- Produces: `sendMail(conn smtpConn, to, subject, body string) error` — сам собирает MIME-сообщение (`From`, `To`, `Subject`, `Content-Type` заголовок — формат без изменений) и выполняет всю SMTP-сессию

- [ ] **Step 1: Создать `mailer.go`**

```go
package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"time"
)

func sendMail(conn smtpConn, to, subject, body string) error {
	mime := "MIME-version: 1.0;\nContent-Type: text/html; charset=\"UTF-8\";\n\n"
	header := "From: RSS Downloader<rss-dl@nikonor.ru>\nTo: " + to + "\nSubject: " + subject + "\n"
	msg := []byte(header + mime + body)

	tlsconfig := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         conn.Host,
	}

	dialer := &net.Dialer{Timeout: 15 * time.Second}
	serverConn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(conn.Host, conn.Port), tlsconfig)
	if err != nil {
		return fmt.Errorf("dial smtp %s:%s: %w", conn.Host, conn.Port, err)
	}
	defer serverConn.Close()

	if err := serverConn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return fmt.Errorf("set smtp deadline: %w", err)
	}

	c, err := smtp.NewClient(serverConn, conn.Host)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}

	auth := smtp.PlainAuth("", conn.Login, conn.Password, conn.Host)
	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}

	if err := c.Mail(conn.Login); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}

	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt: %w", err)
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}

	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("write message: %w", err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("close message: %w", err)
	}

	if err := c.Quit(); err != nil {
		return fmt.Errorf("smtp quit: %w", err)
	}

	return nil
}
```

Ключевые изменения против `sendDigest`: `tls.Dial` без таймаута заменён на `tls.DialWithDialer` с таймаутом подключения 15с; после установки соединения — `SetDeadline` на 30с на всю сессию (auth/mail/rcpt/data); `defer serverConn.Close()`; `c.Quit()` проверяется; `to` приходит аргументом (вместо глобальной `email`).

- [ ] **Step 2: Удалить `sendDigest` из `rss-downloader.go`**

Удалить всю функцию `func sendDigest(smtp_conn smtp_conn_type, msg []byte) error` и её комментарий-подпись `// copy & past https://gist...`.

- [ ] **Step 3: Заменить блок отправки в `main()`**

В `main()` заменить фрагмент от строки

```go
			mime := "MIME-version: 1.0;\nContent-Type: text/html; charset=\"UTF-8\";\n\n"
			header := "From: RSS Downloader<rss-dl@nikonor.ru>\nTo: " + email + "\nSubject: " + title + " by RSS Downloader\n"
			msg := []byte(header + mime + body)
			err := sendDigest(smtp_conn, msg)
			if err != nil {
				log.Fatal(err)
			} else {
				fmt.Println("message was send", newdate)
				err := updateConfig(configFile, title, "lastPubDate", newdate)
				if err != nil {
					log.Fatal(err)
				}

			}
```

до конца блока на:

```go
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
```

`log.Fatal(err)` на ошибке SMTP убран: сбой отправки логируется, остальные фи́ды обрабатываются, конфиг не обновляется. Разбор HTML-строки по `\n` и regex-заплатка на этот момент ещё на месте — они уходят в Task 3.

- [ ] **Step 4: Проверить сборку**

Run: `go build ./... && go vet ./...`
Expected: без ошибок (удалить из импортов `rss-downloader.go` всё, что стало неиспользуемым: `crypto/tls`, `net/smtp` и др.).

- [ ] **Step 5: Коммит**

Run: `git add -A && git commit -m "mailer: add timeouts, log smtp errors instead of fatal"`

---

### Task 3: `digest.go` — рендер письма из типов gofeed

**Files:**
- Create: `digest.go`
- Modify: `rss-downloader.go` — удалить структуры `Item`, `Channel`, `Feed`, методы `Feed.Parse`, `Feed.String`, функции `parseFeed`, `prepDate`; переписать `getData` в `fetchDigest`; переписать цикл в `main()` на `close(ch)` + `for range` без разбора HTML

**Interfaces:**
- Consumes: `link` из Task 1, `sendMail` из Task 2, `gofeed.NewParser().ParseURLWithContext`
- Produces:
  - `type digest struct { name string; body string }` — передаётся по каналу вместо сырого фида
  - `renderDigest(name string, feed *gofeed.Feed, lastPubDate time.Time) string` — `""`, если новых предметов нет
  - `fetchDigest(wg *sync.WaitGroup, l link, ch chan<- digest)` — горутина: fetch + render + запись в канал (не пишет в канал при ошибке fetch)

- [ ] **Step 1: Создать `digest.go`**

```go
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
```

Детали:
- Шаблон — прежний `Feed.String` **без двух первых строк-заголовков** (`{{.Title}}` / `{{.PubDate}}`), которые ранее вытаскивались из HTML в `main()`. Имя фида в шапке — `{{.Name}}` (название секции из конфига, как было).
- Дата предмета — настоящая дата публикации из `item.PublishedParsed` в поясе MSK, формат `2006-01-02 15:04` (единственное содержательное отличие писем, предусмотренное спецификацией).
- Предметы без даты пропускаются; текст — `item.Description` без экранирования (`template.HTML`).
- `loadMSK` — защита от паники `In(nil)`, если в системе нет tzdata.

- [ ] **Step 2: Удалить мёртвый код из `rss-downloader.go`**

Удалить: `type Item`, `type Channel`, `type Feed`, `func (f *Feed) Parse`, `func (f Feed) String` (вместе с вложенным шаблоном), `func parseFeed`, `func prepDate`.

- [ ] **Step 3: Заменить `getData` на `fetchDigest`**

Удалить `func getData(...)`. `recover()` убирается — паника должна быть видна. Новая функция:

```go
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
```

При ошибке fetch горутина ничего не пишет в канал — поэтому в `main` цикл чтения меняется на `close(ch)` + `for range` (Step 4). Блокировки записи нет: ёмкость канала = `len(conf)`, каждая горутина пишет не более одного `digest`.

- [ ] **Step 4: Переписать цикл в `main()`**

Заменить запуск горутин и весь цикл чтения:

```go
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
```

Исчезают: `strings.SplitN(ss.String(), "\n", 3)`, `title`/`newdate`/`body` из распарсинга, regex-заплатка `&#43;`, патч нулевой даты `0001`. `subject` строится из `d.name`; при успешной отправке в конфиг пишется `time.Now().Format(timeForm2)`; при ошибке отправки конфиг не меняется.

- [ ] **Step 5: Проверить сборку**

Run: `go build ./... && go vet ./...`
Expected: без ошибок. Удалить из импортов `rss-downloader.go` всё неиспользуемое (`encoding/xml`, `bytes`, `html/template`, `regexp`, `strings`, `fmt` — по мере отсутствия использования).

- [ ] **Step 6: Коммит**

Run: `git add -A && git commit -m "digest: render from gofeed types, drop dead code and html parsing"`

---

### Task 4: `main.go` — финальная форма, удаление `rss-downloader.go`

**Files:**
- Create: `main.go`
- Delete: `rss-downloader.go`

**Interfaces:**
- Consumes: `readConfig`, `updateConfig`, `defaultConfigPath` (config.go), `fetchDigest`-логика (переносится), `sendMail` (mailer.go), `renderDigest`, `digest` (digest.go)
- Produces: финальная форма пакета из четырёх файлов; глобальные переменные, `init()` и `rssCount` полностью исчезают

- [ ] **Step 1: Создать `main.go`**

```go
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
```

Отличия от промежуточного `main`: нет `init()` и глобальных (`configFile`, `conf`, `rssCount`, `email`, `smtpCfg`) — путь к конфигу резолвится один раз в `main` и передаётся явно; канал создаётся с ёмкостью `len(links)`; `fetchDigest` перенесён в `main.go` без изменений.

- [ ] **Step 2: Удалить `rss-downloader.go`**

Run: `git rm rss-downloader.go`

- [ ] **Step 3: Проверить сборку и вёрстку**

Run: `go build ./... && go vet ./...`
Expected: без ошибок. Пакет `main` состоит ровно из `main.go`, `config.go`, `digest.go`, `mailer.go`:
Run: `ls *.go`
Expected: 4 файла.

- [ ] **Step 4: Коммит**

Run: `git add -A && git commit -m "main: drop globals and init, remove rss-downloader.go"`

---

### Task 5: Финальная проверка критериев готовности

**Files:** без изменений кода.

- [ ] **Step 1: Сборка, vet, линтер**

```
go build ./...
go vet ./...
golangci-lint run
```

Ожидание: build и vet — без замечаний. Если `golangci-lint` выдаст замечания — не чинить, зафиксировать и отчитаться (правило верификации).

- [ ] **Step 2: Нормальный прогон с реальным конфигом**

```
go build -o /tmp/rss-downloader .
/tmp/rss-downloader .rss-downloader.conf
echo "exit=$?"
```

Ожидание: для каждого фида с новыми предметами письмо ушло (проверить во почте), в `.rss-downloader.conf` обновлены `lastPubDate` в формате `timeForm2` (пример: `2026-09-16 16:46:48 +0300 +0300`), exit 0. Второй прогон сразу после первого — письма нет, в логе `no new items` (предметов с датой после только что записанного `lastPubDate` нет).

- [ ] **Step 3: Битый URL**

Создать `/tmp/bad.conf`:

```ini
[DEFAULT]
email=nikonor@nikonor.ru
smtp_login=e@mail
smtp_passwd=PasSwOrD
smtp_server=smtp.yandex.ru:465

[broken]
url=http://127.0.0.1:1/feed
```

```
/tmp/rss-downloader /tmp/bad.conf
echo "exit=$?"
```

Ожидание: в логе строка вида `feed broken: fetch: ...`, программа не зависает (fetch не длиннее ~5с), exit 0, конфиг не изменён.

- [ ] **Step 4: Недостижимый SMTP**

Создать `/tmp/bad-smtp.conf` (рабочий фид, `smtp_server=127.0.0.1:9`):

```ini
[DEFAULT]
email=nikonor@nikonor.ru
smtp_login=e@mail
smtp_passwd=PasSwOrD
smtp_server=127.0.0.1:9

[Lifehacker.ru]
url=https://lifehacker.ru/feed/
lastPubDate=2015-01-01 00:00:01 +0000 GMT
```

```
time /tmp/rss-downloader /tmp/bad-smtp.conf
echo "exit=$?"
git diff --no-index /dev/null /tmp/bad-smtp.conf >/dev/null 2>&1; cat /tmp/bad-smtp.conf
```

Ожидание: в логе строка вида `feed Lifehacker.ru: send: dial smtp 127.0.0.1:9: ...` примерно через 15 секунд, программа не зависает, exit 0, `lastPubDate` в конфиге **не изменён** (осталось `2015-01-01 ...`).

- [ ] **Step 5: Финальный отчёт**

Отчитаться: все 4 критерия готовности спецификации (build/vet, реальный прогон, битый URL, мёртвый SMTP) выполнены, указать фактические строки логов.
