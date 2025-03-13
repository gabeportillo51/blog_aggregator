package config

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gabeportillo51/blog_aggregator/internal/database"
	"github.com/google/uuid"
)

type Config struct {
	DBUrl string `json:"db_url"`
	User  string `json:"current_user_name"`
}

type State struct {
	Db  *database.Queries
	Cfg *Config
}

type Command struct {
	Name string
	Args []string
}

type Commands struct {
	Registry map[string]func(*State, Command) error
}

type RSSFeed struct {
	Channel struct {
		Title       string    `xml:"title"`
		Link        string    `xml:"link"`
		Description string    `xml:"description"`
		Item        []RSSItem `xml:"item"`
	} `xml:"channel"`
}

type RSSItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
}

func FetchFeed(ctx context.Context, feedURL string) (*RSSFeed, error) {
	request, err := http.NewRequestWithContext(ctx, "GET", feedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	request.Header.Set("User-Agent", "gator")
	client := &http.Client{}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("error getting response: %w", err)
	}
	defer response.Body.Close()
	responseBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %w", err)
	}
	rssfeed := &RSSFeed{}
	err = xml.Unmarshal(responseBytes, rssfeed)
	if err != nil {
		return nil, fmt.Errorf("error unmarshaling: %w", err)
	}
	rssfeed.Channel.Title = html.UnescapeString(rssfeed.Channel.Title)
	rssfeed.Channel.Description = html.UnescapeString(rssfeed.Channel.Description)
	for i := range rssfeed.Channel.Item {
		rssfeed.Channel.Item[i].Title = html.UnescapeString(rssfeed.Channel.Item[i].Title)
		rssfeed.Channel.Item[i].Description = html.UnescapeString(rssfeed.Channel.Item[i].Description)
	}
	return rssfeed, nil
}

func (c *Commands) Register(name string, f func(*State, Command) error) {
	c.Registry[name] = f
}

func (c *Commands) Run(s *State, cmd Command) error {
	f, ok := c.Registry[cmd.Name]
	if !ok {
		return errors.New("that command doesn't exist within the command registry")
	}
	return f(s, cmd)
}

func HandlerAgg(s *State, cmd Command) error {
	if len(cmd.Args) != 0 {
		return errors.New("error: arguments were provided after the command 'agg'")
	}
	feed, err := FetchFeed(context.Background(), "https://www.wagslane.dev/index.xml")
	if err != nil {
		return fmt.Errorf("an error occured while trying to fetch feed: %w", err)
	}
	fmt.Println(feed)
	return nil
}

func HandlerAddFeed(s *State, cmd Command) error {
	if len(cmd.Args) != 2 {
		return errors.New("error: incorrect number of arguments provided to the 'addfeed' command")
	}
	current_user := s.Cfg.User
	user, _ := s.Db.GetUser(context.Background(), current_user)
	feed := database.CreateFeedParams{
		ID:        uuid.New(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Name:      cmd.Args[0],
		Url:       cmd.Args[1],
		UserID:    user.ID,
	}
	new_feed, err := s.Db.CreateFeed(context.Background(), feed)
	if err != nil {
		return fmt.Errorf("error occured while creating feed: %w", err)
	}
	fmt.Printf("Feed ID: %d\n", new_feed.ID)
	fmt.Printf("Feed created at: %v\n", new_feed.CreatedAt)
	fmt.Printf("Feed updated at: %v\n", new_feed.UpdatedAt)
	fmt.Printf("Feed name: %s\n", new_feed.Name)
	fmt.Printf("Feed url: %s\n", new_feed.Url)
	fmt.Printf("Feed UserID: %v\n", new_feed.UserID)
	return nil
}

func HandlerLogin(s *State, cmd Command) error {
	if len(cmd.Args) != 1 {
		return errors.New("error: either no username was provided or too many usernames were provided")
	}
	_, err := s.Db.GetUser(context.Background(), cmd.Args[0])
	if err != nil {
		return fmt.Errorf("the user '%s' doesn't exist", cmd.Args[0])
	}
	s.Cfg.SetUser(cmd.Args[0])
	fmt.Printf("You are now logged in as: %s\n", cmd.Args[0])
	return nil
}

func HandlerReset(s *State, cmd Command) error {
	if len(cmd.Args) != 0 {
		return errors.New("error: Arguments provided after 'reset' command")
	}
	err := s.Db.ResetUsers(context.Background())
	if err != nil {
		return errors.New("error ocurred while resetting users table")
	}
	fmt.Println("Table 'users' successfully reset.")
	return nil
}

func HandlerListUsers(s *State, cmd Command) error {
	if len(cmd.Args) != 0 {
		return errors.New("error: Arguments provided after 'users' command")
	}
	usrs, err := s.Db.ListUsers(context.Background())
	if err != nil {
		return errors.New("error occurred when trying to list all users")
	}
	current_user := s.Cfg.User
	for _, usr := range usrs {
		if usr == current_user {
			fmt.Printf("* %s (current)\n", usr)
		} else {
			fmt.Printf("* %s\n", usr)
		}
	}
	return nil
}

func HandlerRegister(s *State, cmd Command) error {
	if len(cmd.Args) != 1 {
		return errors.New("error: either no username was provided or too many usernames were provided")
	}
	cxt := context.Background()
	usr := database.CreateUserParams{
		ID:        uuid.New(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Name:      cmd.Args[0],
	}
	_, err := s.Db.CreateUser(cxt, usr)
	if err != nil {
		return fmt.Errorf("error: %s", err)
	}
	s.Cfg.SetUser(cmd.Args[0])
	fmt.Printf("Successfully created user: %s\n", cmd.Args[0])
	user, err := s.Db.GetUser(context.Background(), cmd.Args[0])
	if err != nil {
		return fmt.Errorf("error: %s", err)
	}
	fmt.Printf("User's ID: %d\n", user.ID)
	fmt.Printf("User created at: %v\n", user.CreatedAt)
	fmt.Printf("User updated at: %v\n", user.UpdatedAt)
	fmt.Printf("User's name: %s\n", user.Name)
	return nil
}

func Read() Config {
	var config_struct Config
	home_path, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Error getting home directory path: %s\n", err)
		return config_struct
	}
	json_file_path := home_path + "/.gatorconfig.json"
	file, err := os.Open(json_file_path)
	if err != nil {
		fmt.Printf("Error opening gatorconfig.json: %s\n", err)
		return config_struct
	}
	defer file.Close()
	bytes, err := io.ReadAll(file)
	if err != nil {
		fmt.Printf("Error reading json file: %s\n", err)
		return config_struct
	}
	err = json.Unmarshal(bytes, &config_struct)
	if err != nil {
		fmt.Printf("Error decoding json file: %s\n", err)
	}
	return config_struct
}

func (c Config) SetUser(user string) {
	c.User = user
	home_path, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Error getting home directory path: %s\n", err)
		return
	}
	json_file_path := home_path + "/.gatorconfig.json"
	file, err := os.Create(json_file_path)
	if err != nil {
		fmt.Printf("Error opening json file: %s\n", err)
		return
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	err = encoder.Encode(c)
	if err != nil {
		fmt.Printf("Error encoding struct into json: %s\n", err)
		return
	}
}
