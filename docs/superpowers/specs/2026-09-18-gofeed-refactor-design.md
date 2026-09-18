# Дизайн: переработка rss-downloader на прямое использование gofeed

Дата: 2026-09-18
Статус: утверждён

## Контекст

`rss-downloader` — CLI-утилита для cron: читает список RSS-лент из INI-конфига, скачивает новые предметы с момента последнего прогона, собирает HTML-письмо и отправляет его по SMTP. Обновляет в конфиге `lastPubDate` после успешной отправки.

Текущая реализация (один файл `rss-downloader.go`, ~390 строк) содержит:

- собственные структуры `Feed`, `Channel`, `Item` — промежуточное представление, дублирующее типы `gofeed`;
- мёртвый код: `Feed.Parse` (XML-декодирование старым способом, нигде не вызывается), структура `rss`;
- `recover()` в горутинах, который молча глотает паники;
- `log.Fatal` на любой ошибке SMTP — одна сломанная отправка убивает весь прогон;
- `tls.Dial` к SMTP-серверу **без таймаута** — вероятная причина «ханга»: недостижимый SMTP-сервер вешает программу до OS-таймаута TCP;
- хрупкую схему: `main` разбирает отрендеренную HTML-строку по `\n`, чтобы вытащить из неё title и дату (regex-заплатка `&#43;`, patch нулевой даты `0001`);
- игнорируемые ошибки в `updateConfig` (ошибка `WriteFile` отбрасывается) и `readConfig` (`SplitHostPort` без проверки).

`gofeed` (v1.4.2) уже используется для скачивания и парсинга (`ParseURLWithContext`, 5-секундный таймаут корректно передаётся в HTTP-запрос).

## Цель

Убрать собственные структуры фида и работать типами `gofeed` напрямую; удалить мёртвый код; исправить проблемы с хангом и падением прогона. Формат конфига, внешний вид писем и общий сценарий работы не меняются.

## Принятые решения

| Вопрос | Решение |
|---|---|
| Объём | Средний: рефакторинг на gofeed + фиксы найденных проблем. Без переработки конфига, без новой архитектуры |
| Сбой фида (fetch/parse) | Логировать (`log.Printf`) и пропустить. Сводных писем нет, exit code не меняется |
| Сбой SMTP | Логировать и продолжить обработку остальных фидов. Exit code 0. Конфиг при ошибке отправки не обновляется |
| Тесты | Не пишутся. Проверка ручными прогонами |
| Структура кода | Пакет `main`, четыре файла по ответственности: `main.go`, `config.go`, `digest.go`, `mailer.go` |

## Архитектура и поток данных

```
readConfig (INI) → []link{Name, URL, LastPubDate}, email, smtpConn
        │
main:  для каждого link → go fetchDigest(link, ch)
        │                    ├─ gofeed.ParseURLWithContext (таймаут 5с)
        │                    ├─ renderDigest(name, feed, lastPubDate) → HTML или ""
        │                    └─ ch ← digest{name, body}
        │
wg.Wait() → close(ch) → for range ch:
        body == "" → log "no new items", continue
        sendMail(conn, email, subject, body)
            ошибка → log, continue (config НЕ обновляем)
            успех  → updateConfig(filename, name, now)
        │
exit 0 (всегда)
```

Ключевое отличие от текущей схемы: в канал передаётся не сырой `Feed`, а готовый `digest` (имя фида + собранный HTML). `main` не разбирает отрендеренную строку по `\n`: хак с двумя строками-заголовками в выводе шаблона и regex-заплатка исчезают.

## Файлы и компоненты

### `digest.go` — чистая логика, без сети

- `type digest struct { name string; body string }`
- `renderDigest(name string, feed *gofeed.Feed, lastPubDate time.Time) string`:
  - фильтрует предметы: `item.PublishedParsed != nil && item.PublishedParsed.After(lastPubDate)`;
  - если новых предметов нет — возвращает `""`;
  - иначе рендерит HTML через `html/template`. Текст предмета — `template.HTML(item.Description)` (без экранирования, как сейчас).
  - предметы без даты (`PublishedParsed == nil`) пропускаются (текущее поведение).

### `main.go`

- `main()`:
  - резолвит путь к файлу конфига один раз (`os.Args[1]` или `~/.rss-downloader.conf` по умолчанию) и передаёт его явно в `readConfig`/`updateConfig`;
  - `readConfig`;
  - создаёт буферизованный канал `chan digest` ёмкостью `len(links)` (записи никогда не блокируются: каждая горутина пишет не более одного `digest`);
  - запускает `fetchDigest` в горутинах под `sync.WaitGroup`;
  - `wg.Wait()`, `close(ch)`, цикл `for range ch`;
  - для непустого `digest`: `sendMail` → при успехе `updateConfig`; при ошибке отправки — только лог.
- `fetchDigest(link, chan<- digest)`:
  - `context.WithTimeout(context.Background(), 5*time.Second)` на фи́д (как сейчас);
  - `gofeed.NewParser().ParseURLWithContext(url, ctx)`;
  - при ошибке — `log.Printf("feed %s: fetch: %v", name, err)` и возврат (в канал ничего не пишет);
  - `renderDigest` → `ch <- digest{name, body}`.
- **`recover()` убирается**: паника должна быть видна, а не глотаться.
- `init()` и глобальные переменные (`conf`, `rssCount`, `rsses`, `email`, `smtp_conn`) убираются.

### `config.go`

- `type link struct { Name string; URL string; LastPubDate time.Time }`
- `type smtpConn struct { Login, Password, Host, Port string }`
- `readConfig(filename string) (string, smtpConn, []link, error)`:
  - формат INI через `robfig/config` не меняется;
  - секция `DEFAULT`: `email`, `smtp_login`, `smtp_passwd`, `smtp_server`;
  - прочие секции: `url`, `lastPubDate` (понимаются оба существующих формата: `2006-01-02 15:04:05 +0000 MST` и `2006-01-02 15:04:05 +0000 +0000`; пустое или нераспознаваемое значение — нулевое время, фи́д считается новым целиком);
  - `smtp_server` без порта (ошибка `net.SplitHostPort`): host берётся как есть, порт по умолчанию `465`;
  - ошибка чтения файла возвращается вызывающему: в `main` — `log.Fatalf` с текстом ошибки (вместо текущего `panic`).
- `updateConfig(filename, section, key, value string) error`:
  - та же механика (`AddOption`, `WriteFile`), но ошибка `WriteFile` проверяется и возвращается.

### `mailer.go`

- `sendMail(conn smtpConn, to, subject, body string) error`:
  - подключение: `tls.DialWithDialer(&net.Dialer{Timeout: 15*time.Second}, "tcp", "<host>:<port>", tlsconfig)`;
  - после установки соединения: `conn.SetDeadline(time.Now().Add(30*time.Second))` — покрывает всю SMTP-сессию (auth, mail, rcpt, data);
  - `defer conn.Close()`;
  - `c.Quit()` — ошибка проверяется;
  - остальное (PlainAuth, TLS-конфиг с `InsecureSkipVerify: true`, порт 465) без изменений.

### Удаляется

- структуры `Feed`, `Channel`, `Item`, `rss`;
- методы `Feed.Parse`, `Feed.String`;
- функции `parseFeed`, `prepDate`;
- regex-заплатка `&#43;`, patch нулевой даты `0001`;
- константы `timeForm`/`timeForm2` переносятся в `config.go` (используются для чтения конфига).

## Обработка ошибок и таймауты

| Проблема сейчас | Решение |
|---|---|
| `tls.Dial` к SMTP без таймаута → ханг | `tls.DialWithDialer` (таймаут подключения 15с) + `SetDeadline` 30с на сессию |
| `recover()` глотает паники | убирается |
| `log.Fatal` на ошибке SMTP роняет прогон | `log.Printf` + continue |
| `c.Quit()` без проверки, `conn.Close()` не вызывается | проверить Quit, `defer conn.Close()` |
| `updateConfig`: ошибка `WriteFile` отбрасывается | проверять реальную ошибку записи, возвращать её |
| нечитаемый конфиг → `panic` | `log.Fatalf` с текстом ошибки |
| `smtp_server` без порта молча ломает адрес | порт по умолчанию `465` |
| ошибка fetch/фида | `log.Printf`, continue |
| таймаут fetch | 5с на фи́д, как сейчас |

Программа во всех нештатных ситуациях (битые URL, недостижимые фи́ды, сбитый SMTP) завершается с кодом 0, оставляя диагностические строки в логе.

## Семантика `lastPubDate`

- Фильтр новых предметов: `PublishedParsed.After(lastPubDate)`.
- При **успешной** отправке письма в конфиг пишется `lastPubDate = time.Now()` (формат `2006-01-02 15:04:05 +0000 +0000`). Это фактическое текущее поведение (из-за не заполняемой даты канала программа всегда записывала «сейчас»), теперь — без хак-ов.
- При **ошибке** отправки конфиг не изменяется: предметы не потеряются и будут отправлены в следующем прогоне.

## Внешний вид писем

- HTML-шаблон, `From: RSS Downloader<rss-dl@nikonor.ru>`, `Subject: <имя фида> by RSS Downloader`, MIME-заголовок — без изменений.
- Один email на фи́д с новыми предметами.
- Единственное содержательное отличие: **дата у каждого предмета** — настоящая дата публикации (из `PublishedParsed`, в часовом поясе MSK, формат `2006-01-02 15:04`), а не общая «дата прогона», как сейчас.
- Текст предмета — `Description` (переход на `Content`/`content:encoded` — вне рамок, при необходимости отдельно).

## Критерии готовности

Тесты не пишутся; проверка ручными прогонами:

1. `go build ./...` и `go vet ./...` — без замечаний.
2. Прогон с реальным `.rss-downloader.conf` — письма ушли, в конфиге обновлён `lastPubDate`.
3. Прогон с битым URL в конфиге — в логе ошибка фида, программа не зависает, exit 0, остальные фи́ды обработаны.
4. Прогон с недостижимым SMTP (закрытый порт) — в логе ошибка отправки через ~15с, программа не зависает, exit 0, конфиг не изменён.

## Не в рамах

- смена формата конфига и/или библиотеки `robfig/config`;
- переход текста предмета с `Description` на `Content`;
- RFC 2047-кодирование русского subject'а;
- сводные письма об ошибках, алерты, ненулевые exit codes;
- тесты;
- пересмотр TLS-конфига SMTP (`InsecureSkipVerify`).
