package main

import (
	"fmt"
	"io"
	"log"
	"net/url"
	"strings"

	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/nobe4/slacked/internal/markdown"
	"github.com/nobe4/slacked/internal/slackclient"
)

func main() {
	a := app.New()
	w := a.NewWindow("Hello")

	input := widget.NewEntry()
	input.SetPlaceHolder("slack url")

	output := widget.NewTextGrid()

	w.SetContent(container.NewVBox(
		input,
		widget.NewButton("Archive", func() {
			h, err := readSlack(input.Text)
			if err != nil {
				panic(err)
			}
			output.SetText(h)
		}),
		widget.NewSeparator(),
		output,
		widget.NewButton("Copy", func() {
			w.Clipboard().SetContent(output.Text())
		}),
	))

	w.ShowAndRun()
}

type UserProvider struct{}

func (u UserProvider) UsernameForID(id string) (string, error) {
	return id, nil
}

func readSlack(link string) (string, error) {
	linkParts, err := parsePermalink(link)
	if err != nil {
		return "", err
	}

	logger := log.New(io.Discard, "", log.LstdFlags)

	client, err := slackclient.New(linkParts.team, logger)
	if err != nil {
		return "", err
	}

	// TODO: 10should be an input
	history, err := client.History(linkParts.channelID, linkParts.timestamp, linkParts.thread, 10)
	if err != nil {
		return "", err
	}

	output, err := markdown.FromMessages(client, history)
	if err != nil {
		return "", err
	}
	return output, nil
}

type linkParts struct {
	team      string
	channelID string
	timestamp string
	thread    string
}

func parsePermalink(link string) (linkParts, error) {
	u, err := url.Parse(link)
	if err != nil {
		return linkParts{}, err
	}

	team, ok := strings.CutSuffix(u.Hostname(), ".slack.com")
	if !ok {
		return linkParts{}, fmt.Errorf("expected slack.com subdomain: %q", link)
	}

	pathSegments := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(pathSegments) != 3 || pathSegments[0] != "archives" {
		return linkParts{}, fmt.Errorf("expected path of the form /archives/<channel>/p<timestamp>: %q", link)
	}

	channel := pathSegments[1]
	timestamp := pathSegments[2][1:len(pathSegments[2])-6] + "." + pathSegments[2][len(pathSegments[2])-6:]

	return linkParts{
		team:      team,
		channelID: channel,
		timestamp: timestamp,
		thread:    u.Query().Get("thread_ts"),
	}, nil
}
