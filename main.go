package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/grafov/m3u8"
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
	Title    string `json:"title"`
	Location string `json:"location"`
}

type ReplicaRequest struct {
	Url string `json:"url"`
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

func downloadHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not supported", http.StatusMethodNotAllowed)
			return
		}

		var req ReplicaRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		if req.Url == "" {
			http.Error(w, "Missing URL in request body", http.StatusBadRequest)
			return
		}
		filename := filepath.Base(req.Url)
		u, err := url.Parse(req.Url)
		if err != nil {
			fmt.Println("Error parsing URL:", err)
			return
		}
		baseURL := u.Scheme + "://" + u.Host

		name := strings.TrimSuffix(filename, ".m3u8")
		dir := filepath.Join("videos", name)
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			fmt.Printf("Error creating directory %s: %v\n", dir, err)
			return
		}
		path := fmt.Sprintf("videos/%s/%s", name, filename)
		fmt.Println(path)

		if !FileExistsByTitle(db, name) {
			_, err := db.Exec("INSERT INTO videos (title, path) VALUES (?, ?)", name, path)
			if err != nil {
				log.Printf("Error inserting data: %v", err)
			} else {
				log.Printf("Added %s to database", name)
			}
		} else {
			log.Printf("%s is already in the database", name)
		}
		output := filepath.Join(dir, filename)

		resp, err := http.Get(req.Url)
		if err != nil {
			fmt.Printf("Error downloading file: %v\n", err)
			return
		}
		defer resp.Body.Close()

		file, err := os.Create(output)
		if err != nil {
			fmt.Printf("Error creating output file: %v\n", err)
			return
		}
		defer file.Close()

		_, err = io.Copy(file, resp.Body)
		if err != nil {
			fmt.Printf("Error writing to file: %v\n", err)
			return
		}

		fmt.Printf("File downloaded and saved to %s\n", output)

		m3u8Content, err := ioutil.ReadFile(output)
		if err != nil {
			fmt.Printf("Error reading m3u8 file: %v\n", err)
			return
		}

		playlist, listType, err := m3u8.DecodeFrom(bufio.NewReader(strings.NewReader(string(m3u8Content))), true)
		if err != nil {
			fmt.Printf("Error decoding m3u8 playlist: %v\n", err)
			return
		}

		if listType == m3u8.MEDIA {
			mediaPlaylist := playlist.(*m3u8.MediaPlaylist)
			for _, segment := range mediaPlaylist.Segments {
				if segment != nil && segment.URI != "" {
					output = filepath.Join(dir, segment.URI)
					segmenturl := fmt.Sprintf("%s/%s/%s", baseURL, name, segment.URI)
					resp, err := http.Get(segmenturl)
					if err != nil {
						fmt.Printf("Error downloading file: %v\n", err)
						return
					}
					defer resp.Body.Close()

					file, err := os.Create(output)
					if err != nil {
						fmt.Printf("Error creating output file: %v\n", err)
						return
					}
					defer file.Close()

					_, err = io.Copy(file, resp.Body)
					if err != nil {
						fmt.Printf("Error writing to file: %v\n", err)
						return
					}

					fmt.Printf("File segment downloaded and saved to %s\n", output)
				}
			}
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Downloaded and saved files successfully"))
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
	http.HandleFunc("/download", downloadHandler(db))
	http.HandleFunc("/videos", getAllVideosHandler(db))

	fmt.Printf("Starting server on %v\n", port)
	log.Printf("Serving %s on HTTP port: %v\n", dir, port)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%v", port), nil))
}
