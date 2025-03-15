package config

import (
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
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

func ScrapeFeeds(s *State) error {
	feed, err := s.Db.GetNextFeedToFetch(context.Background())
	if err != nil {
		return errors.New("error getting next feed to fetch")
	}
	currentTime := sql.NullTime{
		Time:  time.Now(),
		Valid: true,
	}
	mark := database.MarkFeedFetchedParams{
		UpdatedAt:     time.Now(),
		LastFetchedAt: currentTime,
		ID:            feed.ID,
	}
	err1 := s.Db.MarkFeedFetched(context.Background(), mark)
	if err1 != nil {
		return errors.New("error marking feed")
	}
	rssfeed, err2 := FetchFeed(context.Background(), feed.Url)
	if err2 != nil {
		return errors.New("error fetching feed")
	}
	for _, item := range rssfeed.Channel.Item {
		description := sql.NullString{
			String: item.Description,
			Valid:  true,
		}
		defaultTime := time.Now()
		publishedTime := defaultTime
		parsedTime, err := time.Parse(time.RFC1123, item.PubDate)
		if err == nil {
			publishedTime = parsedTime
		} else {
			parsedTime, err = time.Parse(time.RFC3339, item.PubDate)
			if err == nil {
				publishedTime = parsedTime
			} else {
				parsedTime, err = time.Parse(time.RFC850, item.PubDate)
				if err == nil {
					publishedTime = parsedTime
				} else {
					parsedTime, err = time.Parse(time.RFC1123Z, item.PubDate)
					if err == nil {
						publishedTime = parsedTime
					} else {
						parsedTime, err = time.Parse(time.RFC822Z, item.PubDate)
						if err == nil {
							publishedTime = parsedTime
						} else {
							fmt.Println("Error parsing publish date:", err)
						}
					}
				}
			}
		}
		post := database.CreatePostParams{
			ID:          uuid.New(),
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
			Title:       item.Title,
			Url:         item.Link,
			Description: description,
			PublishedAt: publishedTime,
			FeedID:      feed.ID,
		}
		_, err1 := s.Db.CreatePost(context.Background(), post)
		if err1 != nil {
			if strings.Contains(err1.Error(), "duplicate") || strings.Contains(err1.Error(), "unique constraint") {
				continue
			} else {
				fmt.Println("error creating post")
			}
		}
	}
	return nil
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

func HandlerBrowse(s *State, cmd Command, user database.User) error {
	var limit int32
	limit = 2
	if len(cmd.Args) == 1 {
		res, err := strconv.ParseInt(cmd.Args[0], 10, 32)
		if err != nil {
			return errors.New("argument provided is not an integer")
		} else {
			limit = int32(res)
		}
	}
	getposts := database.GetPostsForUserParams{
		UserID: user.ID,
		Limit:  limit,
	}
	posts, err := s.Db.GetPostsForUser(context.Background(), getposts)
	if err != nil {
		return errors.New("error getting posts")
	}
	for _, post := range posts {
		feed_origin, err := s.Db.GetFeedFromID(context.Background(), post.FeedID)
		if err != nil {
			return err
		}
		fmt.Printf("Post Title: %s\n", post.Title)
		fmt.Printf("Post origin feed: %s\n", feed_origin.Name)
		fmt.Printf("Description: %v\n\n", post.Description.String)
	}
	return nil
}

func HandlerAgg(s *State, cmd Command) error {
	if len(cmd.Args) != 1 {
		return errors.New("incorrect amount of arguments provided to the 'agg' command")
	}
	time_duration, err := time.ParseDuration(cmd.Args[0])
	if err != nil {
		return errors.New("error parsing time duration")
	}
	fmt.Printf("Collecting feeds every %v\n", time_duration)
	ticker := time.NewTicker(time_duration)
	for ; ; <-ticker.C {
		err := ScrapeFeeds(s)
		if err != nil {
			return errors.New("error scraping feeds")
		}
	}
}

func HandlerAddFeed(s *State, cmd Command, user database.User) error {
	if len(cmd.Args) != 2 {
		return errors.New("error: incorrect number of arguments provided to the 'addfeed' command")
	}
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
	fmt.Printf("Feed '%s' successfully created\n", new_feed.Name)
	feed_follow := database.CreateFeedFollowParams{
		ID:        uuid.New(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		UserID:    user.ID,
		FeedID:    new_feed.ID,
	}
	_, err1 := s.Db.CreateFeedFollow(context.Background(), feed_follow)
	if err1 != nil {
		return errors.New("error creating new feed-follow")
	}
	return nil
}

func HandlerFeeds(s *State, cmd Command) error {
	if len(cmd.Args) != 0 {
		return errors.New("error: incorrect number of arguments provided to the 'feeds' command")
	}
	feeds, err := s.Db.ListFeeds(context.Background())
	if err != nil {
		return errors.New("error listing feeds")
	}
	if len(feeds) == 0 {
		fmt.Println("There are currently no feeds.")
		return nil
	} else {
		for _, feed := range feeds {
			fmt.Printf("Feed Name: %v, URL: %v, Created by: %v\n\n", feed.Name, feed.Url, feed.Name_2.String)
		}
	}
	return nil
}

func HandlerFollow(s *State, cmd Command, user database.User) error {
	if len(cmd.Args) != 1 {
		return errors.New("error: incorrect amount of arguments provided to 'follow' command")
	}
	url := cmd.Args[0]
	feed, err := s.Db.GetFeed(context.Background(), url)
	if err != nil {
		return errors.New("error getting feed from provided url")
	}
	new_feed_follow := database.CreateFeedFollowParams{
		ID:        uuid.New(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		UserID:    user.ID,
		FeedID:    feed.ID,
	}
	feed_follow, err := s.Db.CreateFeedFollow(context.Background(), new_feed_follow)
	if err != nil {
		return errors.New("error creating feed-follow entry")
	}
	fmt.Printf("'%v' is now following the feed '%v'\n", feed_follow.UserName, feed_follow.FeedName)
	return nil
}

func HandlerFollowing(s *State, cmd Command, user database.User) error {
	if len(cmd.Args) != 0 {
		return errors.New("error: incorrect number of arguments provided to 'following' command")
	}
	feed_follows, err := s.Db.GetFeedFollowsForUser(context.Background(), user.ID)
	if err != nil {
		return errors.New("error getting feed-follows for user")
	}
	if len(feed_follows) == 0 {
		fmt.Println("You are currently not following any feeds.")
		return nil
	} else {
		for _, feed_follow := range feed_follows {
			fmt.Printf("%v\n", feed_follow.FeedName)
		}
	}
	return nil
}

func MiddlewareLoggedIn(handler func(s *State, cmd Command, user database.User) error) func(*State, Command) error {
	return func(s *State, cmd Command) error {
		current_user := s.Cfg.User
		user, err := s.Db.GetUser(context.Background(), current_user)
		if err != nil {
			return errors.New("error getting user")
		}
		return handler(s, cmd, user)
	}
}

func HandlerUnfollow(s *State, cmd Command, user database.User) error {
	if len(cmd.Args) != 1 {
		return errors.New("incorrect number of arguments provided to 'unfollow' command")
	}
	url := cmd.Args[0]
	feed, err := s.Db.GetFeed(context.Background(), url)
	if err != nil {
		return errors.New("error getting feed")
	}
	feed_follow_to_delete := database.DeleteFeedFollowParams{
		UserID: user.ID,
		FeedID: feed.ID,
	}
	err1 := s.Db.DeleteFeedFollow(context.Background(), feed_follow_to_delete)
	if err1 != nil {
		return errors.New("error deleting feed-follow entry")
	}
	fmt.Printf("'%s' has unfollowed the feed '%s'\n", user.Name, feed.Name)
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
	fmt.Println("All tables successfully reset.")
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
	if len(usrs) == 0 {
		fmt.Println("There are currently no users.")
		return nil
	} else {
		for _, usr := range usrs {
			if usr == current_user {
				fmt.Printf("* %s (current)\n", usr)
			} else {
				fmt.Printf("* %s\n", usr)
			}
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
