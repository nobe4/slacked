package slackclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/rneatherway/slack"
)

type Cursor struct {
	NextCursor string `json:"next_cursor"`
}

type CursorResponseMetadata struct {
	ResponseMetadata Cursor `json:"response_metadata"`
}

type Attachment struct {
	ID   int
	Text string
}

type Message struct {
	User        string
	BotID       string `json:"bot_id"`
	Text        string
	Attachments []Attachment
	Ts          string
	Type        string
	ReplyCount  int `json:"reply_count"`
}

type HistoryResponse struct {
	CursorResponseMetadata
	Ok       bool
	HasMore  bool `json:"has_more"`
	Messages []Message
}

type User struct {
	ID   string
	Name string
}

type UsersResponse struct {
	Ok      bool
	Members []User
}

type UsersInfoResponse struct {
	Ok   bool
	User User
}

type Cache struct {
	Channels map[string]string
	Users    map[string]string
}

type SlackClient struct {
	cachePath string
	team      string
	cache     Cache
	client    *slack.Client
	log       *log.Logger
	tz        *time.Location
}

func New(team string, log *log.Logger) (*SlackClient, error) {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dataHome = path.Join(home, ".local", "share")
	}
	cachePath := path.Join(dataHome, "slacked")

	client := slack.NewClient(team)
	err := client.WithCookieAuth()
	if err != nil {
		return nil, err
	}

	c := &SlackClient{
		cachePath: cachePath,
		team:      team,
		client:    client,
		log:       log,
		tz:        time.Now().Location(),
	}

	return c, c.loadCache()
}

func (c *SlackClient) UsernameForMessage(message Message) (string, error) {
	if message.User != "" {
		return c.UsernameForID(message.User)
	}
	if message.BotID != "" {
		return fmt.Sprintf("bot %s", message.BotID), nil
	}
	return "ghost", nil
}

func (c *SlackClient) API(verb, path string, params map[string]string, body []byte) ([]byte, error) {
	return c.client.API(context.TODO(), verb, path, params, body)
}

func (c *SlackClient) get(path string, params map[string]string) ([]byte, error) {
	return c.API("GET", path, params, []byte("{}"))
}

func (c *SlackClient) users() (*UsersResponse, error) {
	body, err := c.get("users.list", nil)
	if err != nil {
		return nil, err
	}

	users := &UsersResponse{}
	err = json.Unmarshal(body, users)
	if err != nil {
		return nil, err
	}

	if !users.Ok {
		return nil, fmt.Errorf("users response not OK: %s", body)
	}

	return users, nil
}

func (c *SlackClient) loadCache() error {
	content, err := os.ReadFile(c.cachePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}

	return json.Unmarshal(content, &c.cache)
}

func (c *SlackClient) History(channelID string, startTimestamp string, thread string, limit int) (*HistoryResponse, error) {
	params := map[string]string{
		"channel":   channelID,
		"ts":        startTimestamp,
		"inclusive": "true",
		"limit":     strconv.Itoa(limit),
	}

	if thread != "" {
		params["ts"] = thread
		params["oldest"] = startTimestamp
	}

	body, err := c.get("conversations.replies", params)
	if err != nil {
		return nil, err
	}

	historyResponse := &HistoryResponse{}
	err = json.Unmarshal(body, historyResponse)
	if err != nil {
		return nil, err
	}

	if !historyResponse.Ok {
		return nil, fmt.Errorf("conversations.replies response not OK: %s", body)
	}

	// If thread was specified, then we are fetching only part of a thread and
	// should remove the first message if it has a reply count as we don't want
	// the root message.
	if thread != "" && historyResponse.Messages[0].ReplyCount != 0 && len(historyResponse.Messages) > 1 {
		historyResponse.Messages = historyResponse.Messages[1:]
	}

	if thread != "" || historyResponse.Messages[0].ReplyCount != 0 {
		// Either we are deliberately fetching a subthread, or an entire thread.
		return historyResponse, nil
	}

	// Otherwise we read the general channel history
	body, err = c.get("conversations.history",
		map[string]string{
			"channel":   channelID,
			"oldest":    startTimestamp,
			"inclusive": "true",
			"limit":     strconv.Itoa(limit)})
	if err != nil {
		return nil, err
	}

	err = json.Unmarshal(body, historyResponse)
	if err != nil {
		return nil, err
	}

	if !historyResponse.Ok {
		return nil, fmt.Errorf("conversations.history response not OK: %s", body)
	}
	c.log.Println(string(body))
	c.log.Printf("%#v", historyResponse)
	return historyResponse, nil
}

func (c *SlackClient) saveCache() error {
	bs, err := json.Marshal(c.cache)
	if err != nil {
		return err
	}

	err = os.MkdirAll(path.Dir(c.cachePath), 0755)
	if err != nil {
		return err
	}

	err = os.WriteFile(c.cachePath, bs, 0644)
	if err != nil {
		return err
	}

	return nil
}

func (c *SlackClient) UsernameForID(id string) (string, error) {
	if name, ok := c.cache.Users[id]; ok {
		return name, nil
	}

	ur, err := c.users()
	if err != nil {
		return "", err
	}

	c.cache.Users = make(map[string]string)
	for _, ch := range ur.Members {
		c.cache.Users[ch.ID] = ch.Name
	}

	err = c.saveCache()
	if err != nil {
		return "", err
	}

	if name, ok := c.cache.Users[id]; ok {
		return name, nil
	}

	body, err := c.get("users.info", map[string]string{"user": id})
	if err != nil {
		return "", fmt.Errorf("no user with id %q: %w", id, err)
	}

	user := &UsersInfoResponse{}
	err = json.Unmarshal(body, user)
	if err != nil {
		return "", err
	}

	if !user.Ok {
		return "", errors.New("users.info response not OK")
	}

	c.cache.Users[id] = user.User.Name
	err = c.saveCache()
	if err != nil {
		return "", err
	}

	return user.User.Name, nil
}

func (c *SlackClient) GetLocation() *time.Location {
	return c.tz
}
