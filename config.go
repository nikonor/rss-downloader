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
