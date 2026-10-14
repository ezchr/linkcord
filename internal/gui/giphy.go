package gui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

var giphyClient = &http.Client{Timeout: 15 * time.Second}

// fetchGiphy gets GIPHY's GIFs of the search q, or trending ones, with the
// user's API key.
func fetchGiphy(key, q string) ([]gifResult, error) {
	v := url.Values{"api_key": {key}, "limit": {"50"}, "rating": {"pg-13"}}
	endpoint := "https://api.giphy.com/v1/gifs/trending?"
	if q != "" {
		v.Set("q", q)
		endpoint = "https://api.giphy.com/v1/gifs/search?"
	}
	resp, err := giphyClient.Get(endpoint + v.Encode())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GIPHY answered %s (is the key right?)", resp.Status)
	}
	var body struct {
		Data []struct {
			URL    string `json:"url"`
			Images struct {
				FixedWidth struct {
					MP4    string `json:"mp4"`
					URL    string `json:"url"`
					Width  string `json:"width"`
					Height string `json:"height"`
				} `json:"fixed_width"`
			} `json:"images"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]gifResult, 0, len(body.Data))
	for _, d := range body.Data {
		fw := d.Images.FixedWidth
		w, _ := strconv.Atoi(fw.Width)
		h, _ := strconv.Atoi(fw.Height)
		out = append(out, gifResult{URL: d.URL, GIF: fw.URL, Video: fw.MP4, Width: w, Height: h})
	}
	return out, nil
}

// fetchTenor gets Tenor's GIFs of the search q, or featured ones, with the
// user's API key.
func fetchTenor(key, q string) ([]gifResult, error) {
	v := url.Values{"key": {key}, "limit": {"50"}, "media_filter": {"tinymp4,gif"}, "client_key": {"linkcord"}}
	endpoint := "https://tenor.googleapis.com/v2/featured?"
	if q != "" {
		v.Set("q", q)
		endpoint = "https://tenor.googleapis.com/v2/search?"
	}
	resp, err := giphyClient.Get(endpoint + v.Encode())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Tenor answered %s (is the key right?)", resp.Status)
	}
	type media struct {
		URL  string `json:"url"`
		Dims []int  `json:"dims"`
	}
	var body struct {
		Results []struct {
			ItemURL string           `json:"itemurl"`
			Media   map[string]media `json:"media_formats"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]gifResult, 0, len(body.Results))
	for _, r := range body.Results {
		res := gifResult{URL: r.ItemURL, GIF: r.Media["gif"].URL, Video: r.Media["tinymp4"].URL}
		if d := r.Media["tinymp4"].Dims; len(d) == 2 {
			res.Width, res.Height = d[0], d[1]
		}
		out = append(out, res)
	}
	return out, nil
}
