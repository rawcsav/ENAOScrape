### ENAO Genre Scraper

A Go script that scrapes music genre information from [Every Noise at Once](https://everynoise.com) and saves it to a CSV file.

#### Overview
This script collects detailed information about music genres, including:
- Genre name and Spotify playlist link
- Visual attributes (font size, color)
- Associated artists
- Similar and opposite genres

The data is scraped concurrently for improved performance and saved in batches to manage memory efficiently.

#### Setup

1. **Ensure you have Go 1.16 or higher installed.**

2. **Clone the repository:**
    ```bash
    git clone https://github.com/rawcsav/ENAOScrape.git
    cd E
    ```

3. **Install dependencies:**
    ```bash
    go get github.com/PuerkitoBio/goquery
    go get golang.org/x/sync/errgroup
    go get golang.org/x/time/rate
    ```

4. **Run the script:**
    ```bash
    go run main.go
    ```

The script will display progress updates and create a `genres.csv` file with the scraped data upon completion.

#### Note
This script is for educational purposes. Please use responsibly and respect the website's terms of service.