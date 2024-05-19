package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

type VideoTitleResponse struct {
	Titles []string `json:"titles"`
}

type Video struct {
    Title string `json:"title"`
    Path  string `json:"path"`
}

type VideoRequest struct {
	Title string `json:"title"`
    Location string `json:"location"`
}

type VideoResponse struct {
	URL string `json:"url"`
}

func InitDB() *sql.DB {
    db, err := sql.Open("sqlite3", "videos.db")
    if err != nil {
        log.Fatal(err)
    }

    _, err = db.Exec(`CREATE TABLE IF NOT EXISTS videos (title TEXT, path TEXT)`)
    if err != nil {
        log.Fatal(err)
    }

    err = filepath.Walk("videos", func(path string, info os.FileInfo, err error) error {
        if err != nil {
            return err
        }

        if filepath.Ext(path) == ".m3u8" {
            title := strings.TrimSuffix(info.Name(), ".m3u8")
            normalizedPath := filepath.ToSlash(path)

            if !FileExistsByTitle(db, title) {
                _, err := db.Exec("INSERT INTO videos (title, path) VALUES (?, ?)", title, normalizedPath)
                if err != nil {
                    log.Printf("Error inserting data: %v", err)
                } else {
                    log.Printf("Added %s to database", title)
                }
            } else {
                log.Printf("%s is already in the database", title)
            }
        }
        return nil
    })

    if err != nil {
        log.Fatalf("Error walking through video directory: %v", err)
    }

    return db
}

func FileExistsByTitle(db *sql.DB, title string) bool {
    var exists bool
    err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM videos WHERE title = ?)", title).Scan(&exists)
    if err != nil {
        log.Fatalf("Error checking if title exists: %v", err)
    }
    return exists
}


func videoLinkHandler(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost {
            http.Error(w, "Method not supported", http.StatusMethodNotAllowed)
            return
        }

        var req VideoRequest
        err := json.NewDecoder(r.Body).Decode(&req)
        if err != nil {
            http.Error(w, err.Error(), http.StatusBadRequest)
            return
        }
        defer r.Body.Close()

        if req.Title == "" {
            http.Error(w, "Missing title in request body", http.StatusBadRequest)
            return
        }

        var videoPath string
        err = db.QueryRow("SELECT path FROM videos WHERE title = ?", req.Title).Scan(&videoPath)
        if err != nil {
            if err == sql.ErrNoRows {
                http.Error(w, "Video not found", http.StatusNotFound)
                return
            } else {
                http.Error(w, "Database error", http.StatusInternalServerError)
                return
            }
        }

        trimmedVideoPath := strings.TrimPrefix(videoPath, "videos/")
        videoURL := fmt.Sprintf("http://%s/%s", r.Host, trimmedVideoPath)

        resp := VideoResponse{URL: videoURL}
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(resp)
    }
}



func getAllVideosHandler(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodGet {
            http.Error(w, "Method not supported", http.StatusMethodNotAllowed)
            return
        }

        rows, err := db.Query("SELECT title FROM videos")
        if err != nil {
            http.Error(w, "Database query error", http.StatusInternalServerError)
            return
        }
        defer rows.Close()

        var titles []string
        for rows.Next() {
            var title string
            if err := rows.Scan(&title); err != nil {
                http.Error(w, "Error scanning video data", http.StatusInternalServerError)
                return
            }
            titles = append(titles, title)
        }

        response := VideoTitleResponse{
            Titles: titles,
        }

        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(response)
    }
}


func addHeaders(h http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		h.ServeHTTP(w, r)
	}
}

func main() {
    var port int
	flag.IntVar(&port, "port", 8080, "Port number")
	flag.Parse()
	const dir = "videos"

	db := InitDB()
    defer db.Close()
	http.Handle("/", addHeaders(http.FileServer(http.Dir(dir))))
	http.HandleFunc("/video", videoLinkHandler(db))
	http.HandleFunc("/videos", getAllVideosHandler(db)) 

	fmt.Printf("Starting server on %v\n", port)
	log.Printf("Serving %s on HTTP port: %v\n", dir, port)
	

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%v", port), nil))
}

