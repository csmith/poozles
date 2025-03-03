package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"
)

type Puzzles struct {
	Index   string
	Puzzles []Puzzle
}
type Puzzle struct {
	ID       string
	Metadata Puzzlemeta
	Content  string
	Files    []string
}

type Puzzlemeta struct {
	Title   string   `yaml:"title"`
	Answers []string `yaml:"answers"`
	Hints   []string `yaml:"hints"`
}

func main() {
	foundPuzzles := getPuzzles()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /main.css", serveFile("layout/main.css"))
	mux.HandleFunc("GET /main.js", serveFile("layout/main.js"))
	mux.HandleFunc("GET /puzzles/{id}", addTrailingSlash)
	mux.HandleFunc("GET /puzzles/{id}/", servePuzzle(foundPuzzles))
	mux.HandleFunc("GET /puzzles/{id}/{file}", servePuzzleFile(foundPuzzles))
	mux.HandleFunc("GET /{$}", serveIndex(foundPuzzles))
	mux.HandleFunc("POST /guess", handleGuess(foundPuzzles))
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", 8080),
		Handler: mux,
	}

	go func() {
		log.Printf("Listening on port %d", 8080)
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server error: %v", err)
		}
		log.Println("Stopped listening")
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c

	shutdownCtx, shutdownRelease := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownRelease()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Failed to shut down HTTP server: %v", err)
	}
}

func addTrailingSlash(writer http.ResponseWriter, request *http.Request) {
	http.Redirect(writer, request, request.URL.String()+"/", http.StatusTemporaryRedirect)
}

func servePuzzleFile(foundPuzzles *Puzzles) func(http.ResponseWriter, *http.Request) {
	return func(writer http.ResponseWriter, request *http.Request) {
		puzzleID := request.PathValue("id")
		index := slices.IndexFunc(foundPuzzles.Puzzles, func(puzz Puzzle) bool {
			return puzz.ID == puzzleID
		})
		if index == -1 {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		fileName := request.PathValue("file")
		if !slices.Contains(foundPuzzles.Puzzles[index].Files, fileName) {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		serveFile("puzzles/"+puzzleID+"/"+fileName)(writer, request)
	}
}

func serveFile(file string) func(writer http.ResponseWriter, request *http.Request) {
	return func(writer http.ResponseWriter, request *http.Request) {
		http.ServeFile(writer, request, file)
	}
}

func serveIndex(foundPuzzles *Puzzles) func(writer http.ResponseWriter, request *http.Request) {
	return func(writer http.ResponseWriter, request *http.Request) {
		templateBytes, err := os.ReadFile("layout/index.html")
		if err != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			fmt.Println("Unable to read layout template")
			fmt.Println(err)
			return
		}
		t, err := template.New("puzzle").Parse(string(templateBytes))
		if err != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			fmt.Println("Unable to create template")
			fmt.Println(err)
			return
		}
		err = t.ExecuteTemplate(writer, "puzzle", Puzzle{Content: foundPuzzles.Index})
		if err != nil {
			fmt.Println("Error executing template")
			fmt.Println(err)
		}
	}
}

func servePuzzle(foundPuzzles *Puzzles) func(writer http.ResponseWriter, request *http.Request) {
	return func(writer http.ResponseWriter, request *http.Request) {
		puzzleID := request.PathValue("id")
		index := slices.IndexFunc(foundPuzzles.Puzzles, func(puzz Puzzle) bool {
			return puzz.ID == puzzleID
		})
		if index == -1 {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		templateBytes, err := os.ReadFile("layout/index.html")
		if err != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		t := template.New("puzzle")
		t.Funcs(template.FuncMap{
			"htmlSafe": func(html string) template.HTML {
				return template.HTML(html)
			},
		})
		t, err = t.Parse(string(templateBytes))
		if err != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		err = t.ExecuteTemplate(writer, "puzzle", foundPuzzles.Puzzles[index])
		if err != nil {
			fmt.Println("Error executing template")
			fmt.Println(err)
		}
	}
}

func handleGuess(foundPuzzles *Puzzles) func(writer http.ResponseWriter, request *http.Request) {
	return func(writer http.ResponseWriter, request *http.Request) {
		puzzle := request.FormValue("puzzle")
		guess := request.FormValue("guess")
		if puzzle == "" || guess == "" {
			writer.WriteHeader(http.StatusBadRequest)
			fmt.Printf("Puzzle or guess is blank")
			return
		}
		index := slices.IndexFunc(foundPuzzles.Puzzles, func(puzz Puzzle) bool {
			return puzz.ID == puzzle
		})
		if index == -1 {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if slices.Contains(foundPuzzles.Puzzles[index].Metadata.Answers, guess) {
			writer.WriteHeader(http.StatusOK)
			return
		}
		writer.WriteHeader(http.StatusNotFound)
	}
}

func getPuzzles() *Puzzles {
	var foundPuzzles = &Puzzles{}
	entries, err := os.ReadDir("./puzzles")
	if errors.Is(err, os.ErrNotExist) {
		log.Fatal("Puzzles folder must exist")
	}
	if err != nil {
		log.Fatal(err)
	}
	indexBytes, err := os.ReadFile("./puzzles/index.html")
	if errors.Is(err, os.ErrNotExist) {
		log.Fatal("puzzles/index.html - not found")
	}
	if err != nil {
		log.Fatal(err)
	}
	foundPuzzles.Index = string(indexBytes)
	for _, e := range entries {
		if e.IsDir() {
			foundPuzzles.Puzzles = append(foundPuzzles.Puzzles, *getPuzzle(e.Name()))
		}
	}
	return foundPuzzles
}

func getPuzzle(path string) *Puzzle {
	indexBytes, err := os.ReadFile("./puzzles/" + path + "/index.html")
	if errors.Is(err, os.ErrNotExist) {
		log.Fatal("puzzles/" + path + "/index.html - not found")
	}
	if err != nil {
		log.Fatal(err)
	}
	frontmatterBytes, contentBytes, err := splitFrontMatter(indexBytes)
	if err != nil {
		log.Fatal(err)
	}
	meta := &Puzzlemeta{}
	err = yaml.Unmarshal(frontmatterBytes, meta)
	if err != nil {
		log.Println("Unable to unmarshall frontmatter")
		log.Fatal(err)
	}
	if meta.Title == "" {
		log.Fatal("Puzzle needs a title")
	}
	if len(meta.Answers) == 0 {
		log.Fatal("Puzzle needs at least one answer")
	}
	var files []string
	entries, err := os.ReadDir("./puzzles/" + path)
	if errors.Is(err, os.ErrNotExist) {
		log.Fatal("Puzzles folder must exist")
	}
	if err != nil {
		log.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() && e.Name() != "index.html" {
			files = append(files, e.Name())
		}
	}
	return &Puzzle{
		ID:       path,
		Metadata: *meta,
		Content:  string(contentBytes),
		Files:    files,
	}
}

func splitFrontMatter(file []byte) ([]byte, []byte, error) {
	if !bytes.HasPrefix(file, []byte("<!--\n")) {
		return nil, nil, errors.New("no frontmatter")
	}
	index := bytes.Index(file, []byte("-->\n"))
	if index == -1 {
		return nil, nil, errors.New("no frontmatter")
	}
	return file[5:index], file[index+4:], nil
}
