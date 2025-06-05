package main

import "fmt"

type Content = map[string]interface{}

func main() {
    eventID := "dummy"
    reactEvent := Content{
        "m.relates_to": map[string]interface{}{
            "rel_type": "m.annotation",
            "event_id": eventID,
            "key":      "✅️",
        },
    }
    fmt.Println(reactEvent)
}

