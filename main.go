package main

import (
    "encoding/json"
    "fmt"
    "io/ioutil"
    "log"
    "os"
    "strings"

    "gopkg.in/yaml.v3"
    "maunium.net/go/mautrix"
    "maunium.net/go/mautrix/event"
    "maunium.net/go/mautrix/id"
)

type MatrixConfig struct {
    Server   string `yaml:"server"`
    Username string `yaml:"username"`
    Password string `yaml:"password"`
    Room     string `yaml:"room"`
}

type Config struct {
    Matrix MatrixConfig `yaml:"matrix"`
}

type TokenStore struct {
    AccessToken string `json:"access_token"`
    UserID      string `json:"user_id"`
    DeviceID    string `json:"device_id"`
}

func loadConfig(path string) (*Config, error) {
    data, err := ioutil.ReadFile(path)
    if err != nil {
        return nil, err
    }
    var cfg Config
    if err := yaml.Unmarshal(data, &cfg); err != nil {
        return nil, err
    }
    return &cfg, nil
}

func loadToken(path string) (*TokenStore, error) {
    data, err := ioutil.ReadFile(path)
    if err != nil {
        return nil, err
    }
    var ts TokenStore
    if err := json.Unmarshal(data, &ts); err != nil {
        return nil, err
    }
    return &ts, nil
}

func saveToken(path string, ts *TokenStore) error {
    data, err := json.MarshalIndent(ts, "", "  ")
    if err != nil {
        return err
    }
    return ioutil.WriteFile(path, data, 0600)
}

func main() {
    cfg, err := loadConfig("config.yaml")
    if err != nil {
        log.Fatalf("Failed to load config: %v", err)
    }

    tokenPath := "token.json"
    var client *mautrix.Client
    var tokenStore *TokenStore

    // Try to load token.json for re-use
    if ts, err := loadToken(tokenPath); err == nil && ts.AccessToken != "" {
        client, err = mautrix.NewClient(cfg.Matrix.Server, id.UserID(ts.UserID), ts.AccessToken)
        if err != nil {
            log.Fatalf("Failed to create Matrix client with stored token: %v", err)
        }
        client.DeviceID = id.DeviceID(ts.DeviceID)
        log.Println("Loaded access token from file.")
    } else {
        // First-time login
        client, err = mautrix.NewClient(cfg.Matrix.Server, "", "")
        if err != nil {
            log.Fatalf("Failed to create Matrix client: %v", err)
        }
        resp, err := client.Login(&mautrix.ReqLogin{
            Type:     "m.login.password",
            User:     cfg.Matrix.Username,
            Password: cfg.Matrix.Password,
        })
        if err != nil {
            log.Fatalf("Failed to login: %v", err)
        }
        tokenStore = &TokenStore{
            AccessToken: resp.AccessToken,
            UserID:      string(resp.UserID),
            DeviceID:    string(resp.DeviceID),
        }
        if err := saveToken(tokenPath, tokenStore); err != nil {
            log.Printf("Warning: Could not save access token: %v", err)
        } else {
            log.Println("Saved access token to file.")
        }
    }

    syncer := client.Syncer.(*mautrix.DefaultSyncer)
    syncer.OnEventType(event.EventMessage, func(ev *event.Event) {
        if ev.Sender == client.UserID {
            return // Ignore bot's own messages
        }
        if ev.RoomID.String() != cfg.Matrix.Room {
            return // Ignore other rooms
        }
        content, ok := ev.Content.Parsed.(*event.MessageEventContent)
        if !ok || content.MsgType != event.MsgText {
            return
        }
        if strings.HasPrefix(content.Body, "!") {
            handleCommand(client, ev.RoomID, content.Body)
        }
    })

    log.Println("Bot is running!")
    err = client.Sync()
    if err != nil {
        log.Fatalf("Sync() returned error: %v", err)
    }
}

func handleCommand(client *mautrix.Client, roomID id.RoomID, body string) {
    cmd := strings.Fields(body)
    if len(cmd) == 0 {
        return
    }
    switch cmd[0] {
    case "!roms":
        client.SendText(roomID, "You invoked the !roms command!")
    }
}

