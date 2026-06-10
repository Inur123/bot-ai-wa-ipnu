package search

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"golang.org/x/net/html"
)

// SearchResult mewakili hasil pencarian dari web
type SearchResult struct {
	Title   string
	Snippet string
	URL     string
}

// YahooSearch melakukan pencarian web menggunakan Yahoo Search dan mengembalikan hasilnya
func YahooSearch(query string) ([]SearchResult, error) {
	urlStr := fmt.Sprintf("https://search.yahoo.com/search?q=%s", url.QueryEscape(query))
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	client := &http.Client{
		Timeout: 15 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search error: status code %d", resp.StatusCode)
	}

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return nil, err
	}

	var results []SearchResult
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "div" {
			isAlgoSR := false
			for _, a := range n.Attr {
				if a.Key == "class" && strings.Contains(a.Val, "algo-sr") {
					isAlgoSR = true
				}
			}
			if isAlgoSR {
				res := parseSingleYahooResult(n)
				if res.Title != "" {
					results = append(results, res)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)

	return results, nil
}

func parseSingleYahooResult(n *html.Node) SearchResult {
	var res SearchResult
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "h3" {
			res.Title = getText(n)
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, attr := range n.Attr {
				if attr.Key == "href" {
					href := attr.Val
					if href != "" && !strings.HasPrefix(href, "javascript:") && !strings.Contains(href, "yahoo.com/page/") {
						if res.URL == "" {
							res.URL = cleanYahooURL(href)
						}
					}
				}
			}
		}
		if n.Type == html.ElementNode && n.Data == "div" {
			for _, attr := range n.Attr {
				if attr.Key == "class" && (strings.Contains(attr.Val, "compText") || strings.Contains(attr.Val, "compDscr")) {
					res.Snippet = getText(n)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)
	return res
}

func cleanYahooURL(rawURL string) string {
	idx := strings.Index(rawURL, "/RU=")
	if idx == -1 {
		return rawURL
	}
	sub := rawURL[idx+4:]
	end := strings.Index(sub, "/")
	if end != -1 {
		sub = sub[:end]
	}
	if decoded, err := url.QueryUnescape(sub); err == nil {
		return decoded
	}
	return rawURL
}

func getText(n *html.Node) string {
	var sb strings.Builder
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)
	return strings.TrimSpace(sb.String())
}
