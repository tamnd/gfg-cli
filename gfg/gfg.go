// Package gfg is the library behind the gfg command line: the HTTP client,
// request shaping, and the typed data models for GeeksforGeeks.
//
// GFG serves article pages server-side with Open Graph meta tags and
// schema.org JSON-LD embedded in the HTML. The client GETs those pages and
// extracts structured fields without a headless browser. Category pages carry
// navigation links the client filters to article-path stubs. The search page
// is pure client-side JavaScript; the search op exits 5 with a message that
// explains the limitation.
package gfg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// Host is the site this client talks to.
const Host = "www.geeksforgeeks.org"

// BaseURL is the root every article and category URL is built from.
const BaseURL = "https://" + Host

// DefaultUserAgent mimics a real desktop browser. GFG's CloudFront CDN serves
// pages to browser User-Agents; a genuine-looking UA keeps responses full.
const DefaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

const (
	DefaultRate    = 500 * time.Millisecond
	DefaultRetries = 3
	DefaultTimeout = 30 * time.Second
)

// Sentinel errors.
var (
	ErrNotFound    = errors.New("not found")
	ErrBlocked     = errors.New("blocked by WAF")
	ErrRateLimited = errors.New("rate limited")
)

// Article is the full record for one GFG article.
type Article struct {
	Slug        string `json:"slug" kit:"id"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty" table:",truncate"`
	Section     string `json:"section,omitempty"`
	Tags        string `json:"tags,omitempty" table:",truncate"`
	Author      string `json:"author,omitempty"`
	Published   string `json:"published,omitempty"`
	Modified    string `json:"modified,omitempty"`
	Image       string `json:"image,omitempty" table:",truncate"`
	URL         string `json:"url"`
}

// ArticleRef is a lightweight stub emitted by related and category commands.
type ArticleRef struct {
	Slug  string `json:"slug" kit:"id"`
	Title string `json:"title,omitempty"`
	URL   string `json:"url"`
}

// Suggestion is one autocomplete result.
type Suggestion struct {
	Query string `json:"query"`
	Term  string `json:"term" kit:"id"`
	URL   string `json:"url,omitempty"`
}

// Topic is a well-known GFG topic from the site navigation.
type Topic struct {
	Slug string `json:"slug" kit:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Client talks to GeeksforGeeks over HTTP.
type Client struct {
	HTTP      *http.Client
	BaseURL   string
	UserAgent string
	Rate      time.Duration
	Retries   int

	mu   sync.Mutex
	last time.Time
}

// NewClient returns a Client with sensible defaults.
func NewClient() *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: DefaultTimeout},
		BaseURL:   BaseURL,
		UserAgent: DefaultUserAgent,
		Rate:      DefaultRate,
		Retries:   DefaultRetries,
	}
}

// GetArticle fetches one article by slug (or URL) and returns an Article.
func (c *Client) GetArticle(ctx context.Context, slug string) (*Article, error) {
	slug = normalizeSlug(slug)
	if slug == "" {
		return nil, ErrNotFound
	}
	u := c.BaseURL + "/" + slug + "/"
	body, err := c.get(ctx, u)
	if err != nil {
		return nil, err
	}
	return parseArticle(body, slug), nil
}

// RelatedArticles fetches one article page and returns the related-article links
// found in the sidebar and inline link grid.
func (c *Client) RelatedArticles(ctx context.Context, slug string, limit int) ([]*ArticleRef, error) {
	slug = normalizeSlug(slug)
	if slug == "" {
		return nil, ErrNotFound
	}
	u := c.BaseURL + "/" + slug + "/"
	body, err := c.get(ctx, u)
	if err != nil {
		return nil, err
	}
	return parseArticleRefs(body, limit), nil
}

// CategoryArticles fetches a GFG category page and extracts article links.
func (c *Client) CategoryArticles(ctx context.Context, slug string, limit int) ([]*ArticleRef, error) {
	slug = normalizeSlug(slug)
	if slug == "" {
		return nil, ErrNotFound
	}
	// Category pages live at /category/<slug>/ or just /<slug>/
	u := c.BaseURL + "/category/" + slug + "/"
	body, err := c.get(ctx, u)
	if err != nil {
		// Try without /category/ prefix.
		u = c.BaseURL + "/" + slug + "/"
		body, err = c.get(ctx, u)
		if err != nil {
			return nil, err
		}
	}
	return parseArticleRefs(body, limit), nil
}

// Suggest calls GFG's autocomplete endpoint. This endpoint is blocked by
// CloudFront from datacenter IPs; the function returns ErrBlocked when the
// WAF fires.
func (c *Client) Suggest(ctx context.Context, prefix string, limit int) ([]*Suggestion, error) {
	u := c.BaseURL + "/gfg-api/v1/suggestions/?q=" + url.QueryEscape(prefix)
	body, err := c.get(ctx, u)
	if err != nil {
		return nil, err
	}
	return parseSuggestions(body, prefix, limit), nil
}

// Topics returns the well-known GFG topic list with no network call.
func Topics() []*Topic {
	type entry struct{ slug, name string }
	entries := []entry{
		{"dsa", "Data Structures and Algorithms"},
		{"algorithms", "Algorithms"},
		{"python", "Python"},
		{"cpp", "C++"},
		{"c", "C"},
		{"java", "Java"},
		{"javascript", "JavaScript"},
		{"data-science", "Data Science"},
		{"machine-learning", "Machine Learning"},
		{"web-dev", "Web Development"},
		{"system-design", "System Design"},
		{"dbms", "DBMS"},
		{"os", "Operating Systems"},
		{"cn", "Computer Networks"},
	}
	out := make([]*Topic, len(entries))
	for i, e := range entries {
		out[i] = &Topic{
			Slug: e.slug,
			Name: e.name,
			URL:  BaseURL + "/category/" + e.slug + "/",
		}
	}
	return out
}

// get fetches a URL with pacing and retries.
func (c *Client) get(ctx context.Context, rawURL string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}
		body, retry, err := c.do(ctx, rawURL)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, fmt.Errorf("get %s: %w", rawURL, lastErr)
}

func (c *Client) do(ctx context.Context, rawURL string) ([]byte, bool, error) {
	c.pace()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json,*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusOK:
		// check for WAF block via content type
		ct := resp.Header.Get("Content-Type")
		b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if err != nil {
			return nil, true, err
		}
		// GFG's API paths return 200 with CloudFront error HTML when blocked.
		if strings.Contains(ct, "text/html") && isWAFBlock(b) {
			return nil, false, ErrBlocked
		}
		return b, false, nil
	case resp.StatusCode == http.StatusForbidden:
		return nil, false, ErrBlocked
	case resp.StatusCode == http.StatusNotFound:
		return nil, false, ErrNotFound
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, true, ErrRateLimited
	case resp.StatusCode >= 500:
		return nil, true, fmt.Errorf("http %d", resp.StatusCode)
	default:
		return nil, false, fmt.Errorf("http %d", resp.StatusCode)
	}
}

// isWAFBlock detects CloudFront WAF error pages embedded in a 200 response.
func isWAFBlock(b []byte) bool {
	return bytes.Contains(b, []byte("Request blocked")) ||
		bytes.Contains(b, []byte("ERROR: The request could not be satisfied")) ||
		bytes.Contains(b, []byte("Generated by cloudfront"))
}

func (c *Client) pace() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Rate <= 0 {
		c.last = time.Now()
		return
	}
	if wait := c.Rate - time.Since(c.last); wait > 0 {
		time.Sleep(wait)
	}
	c.last = time.Now()
}

func backoff(attempt int) time.Duration {
	d := time.Duration(attempt) * 500 * time.Millisecond
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

// --- parsers ---

var (
	metaRE  = regexp.MustCompile(`<meta\s+[^>]*property="([^"]+)"\s+content="([^"]*)"`)
	metaRE2 = regexp.MustCompile(`<meta\s+[^>]*content="([^"]*)"\s+[^>]*property="([^"]+)"`)
)

// parseArticle extracts an Article from a GFG page body.
func parseArticle(body []byte, slug string) *Article {
	art := &Article{
		Slug: slug,
		URL:  BaseURL + "/" + slug + "/",
	}

	// Extract Open Graph and article meta tags.
	metas := extractMetas(body)
	art.Title = metas["og:title"]
	art.Description = metas["og:description"]
	art.Section = metas["article:section"]
	art.Published = metas["article:published_time"]
	art.Modified = metas["article:modified_time"]
	art.Image = metas["og:image"]
	if u := metas["og:url"]; u != "" {
		art.URL = u
		// Update slug from canonical URL.
		if p := pathFromURL(u); p != "" {
			art.Slug = p
		}
	}

	// Collect all article:tag values.
	art.Tags = strings.Join(metaValues(body, "article:tag"), ", ")

	// JSON-LD fills author and supplements other fields.
	fillFromJSONLD(body, art)

	return art
}

// extractMetas pulls property → content pairs from meta tags.
func extractMetas(body []byte) map[string]string {
	out := map[string]string{}
	for _, m := range metaRE.FindAllSubmatch(body, -1) {
		k := string(m[1])
		if _, exists := out[k]; !exists {
			out[k] = string(m[2])
		}
	}
	for _, m := range metaRE2.FindAllSubmatch(body, -1) {
		k := string(m[2])
		if _, exists := out[k]; !exists {
			out[k] = string(m[1])
		}
	}
	return out
}

// metaValues returns all content values for a given property (handles repeated tags).
func metaValues(body []byte, property string) []string {
	var out []string
	seen := map[string]bool{}
	// Match both attribute orderings.
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`<meta\s+[^>]*property="` + regexp.QuoteMeta(property) + `"\s+content="([^"]*)"`),
		regexp.MustCompile(`<meta\s+[^>]*content="([^"]*)"\s+[^>]*property="` + regexp.QuoteMeta(property) + `"`),
	}
	for _, re := range patterns {
		for _, m := range re.FindAllSubmatch(body, -1) {
			v := string(m[1])
			if v != "" && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	return out
}

// articleJSONLD is the subset of JSON-LD fields we read.
type articleJSONLD struct {
	Type          string `json:"@type"`
	Headline      string `json:"headline"`
	DatePublished string `json:"datePublished"`
	DateModified  string `json:"dateModified"`
	Author        struct {
		Name string `json:"name"`
	} `json:"author"`
	About []struct {
		Name string `json:"name"`
	} `json:"about"`
}

// fillFromJSONLD reads the first Article-type JSON-LD block and supplements art.
func fillFromJSONLD(body []byte, art *Article) {
	re := regexp.MustCompile(`<script\s+type="application/ld\+json"[^>]*>([\s\S]*?)</script>`)
	for _, m := range re.FindAllSubmatch(body, -1) {
		var jld articleJSONLD
		if err := json.Unmarshal(m[1], &jld); err != nil {
			continue
		}
		if jld.Type != "Article" {
			continue
		}
		if art.Title == "" && jld.Headline != "" {
			art.Title = jld.Headline
		}
		if art.Author == "" && jld.Author.Name != "" {
			art.Author = jld.Author.Name
		}
		if art.Published == "" && jld.DatePublished != "" {
			art.Published = jld.DatePublished
		}
		if art.Modified == "" && jld.DateModified != "" {
			art.Modified = jld.DateModified
		}
		// Supplement tags from about list.
		if len(jld.About) > 0 && art.Tags == "" {
			names := make([]string, 0, len(jld.About))
			for _, a := range jld.About {
				if a.Name != "" {
					names = append(names, a.Name)
				}
			}
			art.Tags = strings.Join(names, ", ")
		}
		return
	}
}

// parseArticleRefs extracts ArticleRef stubs from a GFG page body.
func parseArticleRefs(body []byte, limit int) []*ArticleRef {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil
	}

	var out []*ArticleRef
	seen := map[string]bool{}

	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		if limit > 0 && len(out) >= limit {
			return
		}
		href, _ := s.Attr("href")
		slug := articleSlugFromHref(href)
		if slug == "" || seen[slug] {
			return
		}
		seen[slug] = true
		title := strings.TrimSpace(s.Text())
		out = append(out, &ArticleRef{
			Slug:  slug,
			Title: title,
			URL:   BaseURL + "/" + slug + "/",
		})
	})
	return out
}

// articleSlugFromHref returns the article slug from an href, or empty if the
// link does not look like a GFG article path.
func articleSlugFromHref(href string) string {
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	// Must be same host or relative path.
	if u.Host != "" && u.Host != Host {
		return ""
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return ""
	}
	// Must look like an article path: one or two path segments, lowercase,
	// alphanumeric and hyphens, no file extensions.
	parts := strings.Split(path, "/")
	if len(parts) < 1 || len(parts) > 2 {
		return ""
	}
	// Exclude known non-article paths.
	skip := map[string]bool{
		"category": true, "tag": true, "login": true, "register": true,
		"courses": true, "about": true, "legal": true, "advertise-with-us": true,
		"campus-training-program": true, "search": true, "videos": true,
		"explore": true, "nation-skill-up": true, "gfg-corporate-solution": true,
		"problem-of-the-day": true, "premium": true,
	}
	if skip[parts[0]] {
		return ""
	}
	// Each part must be slug-like (letters, digits, hyphens).
	slugRE := regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	for _, p := range parts {
		if !slugRE.MatchString(p) {
			return ""
		}
	}
	return path
}

// suggestResponse is the JSON shape of GFG's autocomplete API.
type suggestResponse struct {
	Data []struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	} `json:"data"`
}

// parseSuggestions decodes GFG autocomplete JSON.
func parseSuggestions(body []byte, query string, limit int) []*Suggestion {
	var resp suggestResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	var out []*Suggestion
	for _, d := range resp.Data {
		if limit > 0 && len(out) >= limit {
			break
		}
		out = append(out, &Suggestion{
			Query: query,
			Term:  d.Title,
			URL:   d.URL,
		})
	}
	return out
}

// normalizeSlug converts a URL or slug to the canonical path component
// (section/slug or bare slug), without leading/trailing slashes.
func normalizeSlug(input string) string {
	input = strings.TrimSpace(input)
	if u, err := url.Parse(input); err == nil && u.Host != "" {
		return strings.Trim(u.Path, "/")
	}
	return strings.Trim(input, "/")
}

// pathFromURL extracts the path from a full URL.
func pathFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.Trim(u.Path, "/")
}
