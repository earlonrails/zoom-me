package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"time"

	"github.com/procyon-projects/chrono"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
	"google.golang.org/api/plus/v1"
)

type UserInfo struct {
	Sub           string `json:"sub"`
	Name          string `json:"name"`
	GivenName     string `json:"given_name"`
	FamilyName    string `json:"family_name"`
	Profile       string `json:"profile"`
	Picture       string `json:"picture"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Gender        string `json:"gender"`
}

type ErrorDetails struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func getUserinfo(client *http.Client) (*UserInfo, error) {
	resp, err := client.Get("https://www.googleapis.com/oauth2/v3/userinfo")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var result UserInfo
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Retrieve a token, saves the token, then returns the generated client.
func getClient() *http.Client {
	b, err := ioutil.ReadFile("credentials.json")
	if err != nil {
		log.Fatalf("Unable to read client secret file: %v", err)
	}

	// If modifying these scopes, delete your previously saved token.json.
	config, err := google.ConfigFromJSON(b, plus.UserinfoEmailScope, calendar.CalendarReadonlyScope)
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}

	tokFile := "token.json"
	tok, err := tokenFromFile(tokFile)
	if isExpired(tok) {
		tok, err = config.TokenSource(context.TODO(), tok).Token()
	}
	if err != nil {
		tok = getTokenFromWeb(config)
		saveToken(tokFile, tok)
	}
	return config.Client(context.Background(), tok)
}

func isExpired(token *oauth2.Token) bool {
	if token.Expiry.Before(time.Now()) {
		return true
	}
	return false
}

// Request a token from the web, then returns the retrieved token.
func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser then type the "+
		"authorization code: \n%v\n", authURL)

	var authCode string
	if _, err := fmt.Scan(&authCode); err != nil {
		log.Fatalf("Unable to read authorization code: %v", err)
	}

	tok, err := config.Exchange(context.TODO(), authCode)
	if err != nil {
		log.Fatalf("Unable to retrieve token from web: %v", err)
	}
	return tok
}

// Retrieves a token from a local file.
func tokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tok := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(tok)
	return tok, err
}

// Saves a token to a file path.
func saveToken(path string, token *oauth2.Token) {
	fmt.Printf("Saving credential file to: %s\n", path)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		log.Fatalf("Unable to cache oauth token: %v", err)
	}
	defer f.Close()
	json.NewEncoder(f).Encode(token)
}

func scheduleZoom(when time.Time, link string) chrono.ScheduledTask {
	taskScheduler := chrono.NewDefaultTaskScheduler()
	task, err := taskScheduler.Schedule(func(ctx context.Context) {
		cmd := exec.Command("open", "-a", "Google Chrome", link)
		log.Printf("Opening Chrome with link: %v", link)
		err := cmd.Run()
		if err != nil {
			log.Printf("Command finished with error: %v", err)
		}
	}, chrono.WithStartTime(when.Year(), when.Month(), when.Day(), when.Hour(), when.Minute(), when.Second()))

	if err != nil {
		log.Println("Error scheduling task", err)
	} else {
		log.Printf("Link: %v will open at: %v", link, when)
	}

	return task
}

func grabZoomLink(item *calendar.Event) string {
	link := ""
	if item.ConferenceData != nil {
		for _, entry := range item.ConferenceData.EntryPoints {
			link = parseLink(entry.Uri)
			if link != "" {
				return link
			}
		}
	}

	link = parseLink(item.Description)
	if link != "" {
		return link
	}

	return parseLink(item.Location)
}

func parseLink(text string) string {
	re := regexp.MustCompile(`(https?:\/\/(www\.)?[-a-zA-Z0-9@:%._\+~#=]{1,256}zoom.us\b[-a-zA-Z0-9()@:%_\+.~#?&\/\/=]*)`)
	return string(re.Find([]byte(text)))
}

func getEvents(ctx context.Context, client *http.Client, userinfo *UserInfo) ([]*calendar.Event, error) {
	srv, err := calendar.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		log.Fatalf("Unable to retrieve Calendar client: %v", err)
	}

	t := time.Now().Format(time.RFC3339)
	// attendees.email == userinfo.Email && attendees.responseStatus == "accepted"
	all_events, err := srv.Events.List("primary").ShowDeleted(false).
		SingleEvents(true).TimeMin(t).MaxResults(10).OrderBy("startTime").Do()

	if err != nil {
		return nil, err
	}
	your_events := []*calendar.Event{}
	event_items := all_events.Items

	for i := 0; i < len(event_items); i++ {
		event := event_items[i]
		for j := range event.Attendees {
			attendee := event.Attendees[j]
			if attendee.Email == userinfo.Email && attendee.ResponseStatus == "accepted" {
				your_events = append(your_events, event)
			}
		}
	}

	return your_events, nil
}

func runInLoop() {
	var tasks []chrono.ScheduledTask
	ctx := context.Background()
	client := getClient()
	userinfo, err := getUserinfo(client)
	if err != nil {
		log.Fatalf("Unable to get user's info: %v", err)
		// refresh token here
		// Response: {
		// 	"error": "invalid_grant",
		// 	"error_description": "Token has been expired or revoked."
		// }
	}
	events, err := getEvents(ctx, client, userinfo)
	if err != nil {
		log.Fatalf("Unable to retrieve next ten of the user's events: %v", err)
	}

	fmt.Println("Upcoming events:")
	if len(events) == 0 {
		fmt.Println("No upcoming events found.")
	} else {
		for _, item := range events {
			date := item.Start.DateTime
			if date == "" {
				date = item.Start.Date
			}

			now := time.Now()
			when, err := time.Parse(time.RFC3339, date)
			if err != nil {
				panic("Couldn't parse time")
			}

			if now.After(when) {
				continue
			}

			link := grabZoomLink(item)
			if link != "" {
				task := scheduleZoom(when, link)
				tasks = append(tasks, task)
			}
		}
	}
	<-time.After(8 * time.Hour)
	for _, task := range tasks {
		task.Cancel()
	}
	runInLoop()
}

func main() {
	runInLoop()
}
