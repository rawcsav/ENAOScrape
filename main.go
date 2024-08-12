package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Genre struct {
	Name          string
	Playlist      string
	FontSize      string
	ColorHex      string
	ColorRGB      string
	Top           string
	Left          string
	ArtistWeights []string
	Artists       []string
	SimWeights    []string
	SimGenres     []string
	OppWeights    []string
	OppGenres     []string
}

var (
	limiter    = rate.NewLimiter(rate.Every(50*time.Millisecond), 1)
	httpClient = &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     90 * time.Second,
		},
	}
)

const batchSize = 250

func main() {
	start := time.Now()
	log.Println("Starting the scraping process...")

	genres := scrapeGenreList()
	totalGenres := len(genres)
	log.Printf("Found %d genres to process", totalGenres)

	results := make(chan Genre, batchSize)
	g, ctx := errgroup.WithContext(context.Background())

	workers := runtime.GOMAXPROCS(0)
	semaphore := make(chan struct{}, workers)

	var processedCount int32

	// Start the CSV writer
	csvDone := make(chan struct{})
	go writeResultsToCSV(results, csvDone, totalGenres)

	for _, genre := range genres {
		genre := genre // https://golang.org/doc/faq#closures_and_goroutines
		g.Go(func() error {
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return ctx.Err()
			}

			if err := limiter.Wait(ctx); err != nil {
				return fmt.Errorf("rate limiter error for %s: %v", genre.Name, err)
			}

			genreData, err := scrapeGenreData(ctx, genre.Name)
			if err != nil {
				return fmt.Errorf("error scraping %s: %v", genre.Name, err)
			}

			genre.Playlist = genreData.Playlist
			genre.ArtistWeights = genreData.ArtistWeights
			genre.Artists = genreData.Artists
			genre.SimWeights = genreData.SimWeights
			genre.SimGenres = genreData.SimGenres
			genre.OppWeights = genreData.OppWeights
			genre.OppGenres = genreData.OppGenres

			select {
			case results <- genre:
				atomic.AddInt32(&processedCount, 1)
				if processed := atomic.LoadInt32(&processedCount); processed%100 == 0 || processed == int32(totalGenres) {
					log.Printf("Processed %d/%d genres", processed, totalGenres)
				}
			case <-ctx.Done():
				return ctx.Err()
			}

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		log.Printf("Error during scraping: %v", err)
	}

	close(results)
	<-csvDone // Wait for CSV writing to complete

	log.Printf("Scraping completed in %v", time.Since(start))
}

func writeResultsToCSV(results <-chan Genre, done chan<- struct{}, totalGenres int) {
	defer close(done)

	file, err := os.Create("genres.csv")
	if err != nil {
		log.Fatalf("Cannot create file: %v", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	headers := []string{"Genre", "Playlist", "FontSize", "ColorHex", "ColorRGB", "Top", "Left", "ArtistWeights", "Artists", "SimWeights", "SimGenres", "OppWeights", "OppGenres"}
	if err := writer.Write(headers); err != nil {
		log.Fatalf("Error writing headers: %v", err)
	}

	var batch [][]string
	genreCount := 0

	for genre := range results {
		row := []string{
			genre.Name,
			genre.Playlist,
			genre.FontSize,
			genre.ColorHex,
			genre.ColorRGB,
			genre.Top,
			genre.Left,
			strings.Join(genre.ArtistWeights, "|"),
			strings.Join(genre.Artists, "|"),
			strings.Join(genre.SimWeights, "|"),
			strings.Join(genre.SimGenres, "|"),
			strings.Join(genre.OppWeights, "|"),
			strings.Join(genre.OppGenres, "|"),
		}
		batch = append(batch, row)
		genreCount++

		if len(batch) >= batchSize {
			if err := writer.WriteAll(batch); err != nil {
				log.Printf("Error writing batch: %v", err)
			}
			writer.Flush()
			log.Printf("Wrote batch of %d genres. Total written: %d/%d", len(batch), genreCount, totalGenres)
			batch = batch[:0] // Clear the batch
		}
	}

	// Write any remaining genres
	if len(batch) > 0 {
		if err := writer.WriteAll(batch); err != nil {
			log.Printf("Error writing final batch: %v", err)
		}
		writer.Flush()
		log.Printf("Wrote final batch of %d genres. Total written: %d/%d", len(batch), genreCount, totalGenres)
	}

	log.Printf("Successfully wrote %d/%d genres to CSV", genreCount, totalGenres)
}

func scrapeGenreList() []Genre {
	res, err := httpClient.Get("https://everynoise.com/engenremap.html")
	if err != nil {
		log.Fatalf("Error fetching genre list: %v", err)
	}
	defer res.Body.Close()

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		log.Fatalf("Error parsing genre list: %v", err)
	}

	var genres []Genre
	doc.Find("div.genre.scanme").Each(func(i int, s *goquery.Selection) {
		genreName := strings.TrimSpace(s.Text())
		genreName = strings.TrimSuffix(genreName, "»")
		playlist, _ := s.Find("a").Attr("href")
		style, _ := s.Attr("style")
		fontSize, colorHex, colorRGB, top, left := extractStyleAttributes(style)
		genres = append(genres, Genre{
			Name:     genreName,
			Playlist: playlist,
			FontSize: fontSize,
			ColorHex: colorHex,
			ColorRGB: colorRGB,
			Top:      top,
			Left:     left,
		})
	})

	return genres
}

var (
	fontSizeRe = regexp.MustCompile(`font-size:([^;]+)`)
	colorRe    = regexp.MustCompile(`color:([^;]+)`)
	topRe      = regexp.MustCompile(`top:([^;]+)`)
	leftRe     = regexp.MustCompile(`left:([^;]+)`)
)

func extractStyleAttributes(style string) (fontSize, colorHex, colorRGB, top, left string) {
	if match := fontSizeRe.FindStringSubmatch(style); len(match) > 1 {
		fontSize = strings.TrimSpace(match[1])
	}
	if match := colorRe.FindStringSubmatch(style); len(match) > 1 {
		colorHex = strings.TrimSpace(match[1])
		r, g, b := hexToRGB(colorHex)
		colorRGB = fmt.Sprintf("rgb(%d, %d, %d)", r, g, b)
	}
	if match := topRe.FindStringSubmatch(style); len(match) > 1 {
		top = strings.TrimSpace(match[1])
	}
	if match := leftRe.FindStringSubmatch(style); len(match) > 1 {
		left = strings.TrimSpace(match[1])
	}
	return
}

func hexToRGB(hex string) (r, g, b int) {
	fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b)
	return
}

var (
	artistWeightsMu sync.Mutex
	artistsWeights  = make(map[string]string)
)

func scrapeGenreData(ctx context.Context, genre string) (Genre, error) {
	encodedGenre := url.QueryEscape(strings.ReplaceAll(genre, " ", ""))
	url := fmt.Sprintf("https://everynoise.com/engenremap-%s.html", encodedGenre)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return Genre{}, fmt.Errorf("error creating request for %s: %v", genre, err)
	}

	res, err := httpClient.Do(req)
	if err != nil {
		return Genre{}, fmt.Errorf("error fetching %s: %v", genre, err)
	}
	defer res.Body.Close()

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		return Genre{}, fmt.Errorf("error parsing %s: %v", genre, err)
	}

	playlist := ""
	doc.Find("a").Each(func(i int, s *goquery.Selection) {
		if s.Text() == "playlist" {
			playlist, _ = s.Attr("href")
		}
	})

	var artistWeights, artists, simWeights, oppWeights, simGenres, oppGenres []string

	doc.Find("div.genre.scanme").Each(func(i int, s *goquery.Selection) {
		style, _ := s.Attr("style")
		artist := strings.TrimSuffix(strings.TrimSpace(s.Text()), "»")
		weight := extractWeight(style)

		artistWeightsMu.Lock()
		if existingWeight, ok := artistsWeights[artist]; ok {
			weight = existingWeight
		} else {
			artistsWeights[artist] = weight
		}
		artistWeightsMu.Unlock()

		artistWeights = append(artistWeights, weight)
		artists = append(artists, artist)
	})

	doc.Find("div.genre").Not(".scanme").Each(func(i int, s *goquery.Selection) {
		id, _ := s.Attr("id")
		style, _ := s.Attr("style")
		weight := extractWeight(style)
		genreName := strings.TrimSuffix(strings.TrimSpace(s.Text()), "»")
		if strings.Contains(id, "nearby") {
			simWeights = append(simWeights, weight)
			simGenres = append(simGenres, genreName)
		} else if strings.Contains(id, "mirror") {
			oppWeights = append(oppWeights, weight)
			oppGenres = append(oppGenres, genreName)
		}
	})

	return Genre{
		Playlist:      playlist,
		ArtistWeights: artistWeights,
		Artists:       artists,
		SimWeights:    simWeights,
		OppWeights:    oppWeights,
		SimGenres:     simGenres,
		OppGenres:     oppGenres,
	}, nil
}

func extractWeight(style string) string {
	if match := fontSizeRe.FindStringSubmatch(style); len(match) > 1 {
		return strings.TrimSuffix(strings.TrimSpace(match[1]), "%")
	}
	return ""
}
