package main

import (
    "bufio"
    "database/sql"
    "fmt"
    "log"
    "net/url"
    "os"
    "strings"

    _ "github.com/mattn/go-sqlite3"
)

func main() {
    infile := "linklist.txt"
    dbfile := "links.db"

    file, err := os.Open(infile)
    if err != nil {
        log.Fatalf("Could not open %s: %v", infile, err)
    }
    defer file.Close()

    db, err := sql.Open("sqlite3", dbfile)
    if err != nil {
        log.Fatalf("Could not open SQLite db: %v", err)
    }
    defer db.Close()

    // Create table if not exists
    _, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS files (
            section TEXT,
            console TEXT,
            file TEXT,
            rawurl TEXT PRIMARY KEY
        )
    `)
    if err != nil {
        log.Fatalf("Could not create table: %v", err)
    }

    scanner := bufio.NewScanner(file)
    tx, err := db.Begin()
    if err != nil {
        log.Fatalf("Could not begin transaction: %v", err)
    }
    stmt, err := tx.Prepare("INSERT OR IGNORE INTO files(section, console, file, rawurl) VALUES (?, ?, ?, ?)")
    if err != nil {
        log.Fatalf("Could not prepare insert: %v", err)
    }
    defer stmt.Close()

    const prefix = "https://myrient.erista.me/files/"
    count := 0
    for scanner.Scan() {
        rawurl := scanner.Text()
        if !strings.HasPrefix(rawurl, prefix) {
            continue // skip lines not matching the expected format
        }
        if !strings.HasSuffix(rawurl, ".zip") {
            continue // skip non-zip files
        }
        rel := strings.TrimPrefix(rawurl, prefix)
        parts := strings.SplitN(rel, "/", 3)
        if len(parts) != 3 {
            continue // skip malformed lines
        }
        section, err1 := url.QueryUnescape(parts[0])
        console, err2 := url.QueryUnescape(parts[1])
        filepart, err3 := url.QueryUnescape(parts[2])
        if err1 != nil || err2 != nil || err3 != nil {
            continue // skip lines with bad encoding
        }
        _, err = stmt.Exec(section, console, filepart, rawurl)
        if err != nil {
            log.Printf("Failed to insert: %v", err)
        }
        count++
        if count%10000 == 0 {
            fmt.Printf("Inserted %d rows...\n", count)
        }
    }
    if err := scanner.Err(); err != nil {
        log.Fatalf("Scanner error: %v", err)
    }
    err = tx.Commit()
    if err != nil {
        log.Fatalf("Could not commit transaction: %v", err)
    }
    fmt.Printf("Done! Inserted %d rows.\n", count)
}

