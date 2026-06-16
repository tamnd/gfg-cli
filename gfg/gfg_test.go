package gfg_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gfg-cli/gfg"
)

func TestDefaultConfig(t *testing.T) {
	c := gfg.NewClient()
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
	if c.Rate != gfg.DefaultRate {
		t.Errorf("Rate = %v, want %v", c.Rate, gfg.DefaultRate)
	}
	if c.Retries != gfg.DefaultRetries {
		t.Errorf("Retries = %d, want %d", c.Retries, gfg.DefaultRetries)
	}
	if c.UserAgent == "" {
		t.Error("UserAgent is empty")
	}
}

func TestNewClientNotNil(t *testing.T) {
	c := gfg.NewClient()
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
}

// articleHTML returns a minimal GFG article page with OG meta and JSON-LD.
func articleHTML(title, desc, section, published, tag1, tag2, author string) string {
	jld, _ := json.Marshal(map[string]any{
		"@context":      "https://schema.org",
		"@type":         "Article",
		"headline":      title,
		"datePublished": published,
		"dateModified":  published,
		"author": map[string]any{
			"@type": "Person",
			"name":  author,
		},
		"about": []map[string]any{
			{"@type": "Thing", "name": tag1},
			{"@type": "Thing", "name": tag2},
		},
	})
	return `<!DOCTYPE html><html><head>
<meta property="og:title" content="` + title + `"/>
<meta property="og:description" content="` + desc + `"/>
<meta property="og:url" content="https://www.geeksforgeeks.org/dsa/binary-search/"/>
<meta property="og:image" content="https://media.geeksforgeeks.org/image.png"/>
<meta property="article:section" content="` + section + `"/>
<meta property="article:published_time" content="` + published + `"/>
<meta property="article:modified_time" content="` + published + `"/>
<meta property="article:tag" content="` + tag1 + `"/>
<meta property="article:tag" content="` + tag2 + `"/>
<script type="application/ld+json">` + string(jld) + `</script>
</head><body><h1>` + title + `</h1></body></html>`
}

func TestArticleFromOGMeta(t *testing.T) {
	html := articleHTML("Binary Search", "A searching algorithm", "Algorithms",
		"2014-01-28T13:22:36+00:00", "BinarySearch", "DSA", "GFG Team")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	defer srv.Close()

	c := gfg.NewClient()
	c.BaseURL = srv.URL
	c.Rate = 0
	c.Retries = 0

	art, err := c.GetArticle(context.Background(), "dsa/binary-search")
	if err != nil {
		t.Fatalf("GetArticle: %v", err)
	}
	if art.Title != "Binary Search" {
		t.Errorf("Title = %q, want Binary Search", art.Title)
	}
	if art.Description != "A searching algorithm" {
		t.Errorf("Description = %q, want A searching algorithm", art.Description)
	}
	if art.Section != "Algorithms" {
		t.Errorf("Section = %q, want Algorithms", art.Section)
	}
	if !strings.Contains(art.Tags, "BinarySearch") {
		t.Errorf("Tags = %q, want to contain BinarySearch", art.Tags)
	}
}

func TestArticleFromJSONLD(t *testing.T) {
	html := articleHTML("Merge Sort", "Sorting algorithm", "Algorithms",
		"2020-05-01T00:00:00Z", "Sorting", "DivideAndConquer", "GeeksforGeeks")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	defer srv.Close()

	c := gfg.NewClient()
	c.BaseURL = srv.URL
	c.Rate = 0
	c.Retries = 0

	art, err := c.GetArticle(context.Background(), "algorithms/merge-sort")
	if err != nil {
		t.Fatalf("GetArticle: %v", err)
	}
	if art.Author != "GeeksforGeeks" {
		t.Errorf("Author = %q, want GeeksforGeeks", art.Author)
	}
	if art.Published == "" {
		t.Error("Published is empty")
	}
}

func TestCategoryLinks(t *testing.T) {
	html := `<!DOCTYPE html><html><head>
<meta property="og:url" content="https://www.geeksforgeeks.org/category/dsa/"/>
</head><body>
<a href="/dsa/binary-search/">Binary Search</a>
<a href="/dsa/merge-sort/">Merge Sort</a>
<a href="/login">Login</a>
<a href="/courses/dsa">DSA Course</a>
<a href="https://www.geeksforgeeks.org/python/python-basics/">Python Basics</a>
</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	defer srv.Close()

	c := gfg.NewClient()
	c.BaseURL = srv.URL
	c.Rate = 0
	c.Retries = 0

	refs, err := c.CategoryArticles(context.Background(), "dsa", 0)
	if err != nil {
		t.Fatalf("CategoryArticles: %v", err)
	}
	for _, r := range refs {
		if strings.Contains(r.Slug, "login") || strings.Contains(r.Slug, "courses") {
			t.Errorf("Got non-article slug: %s", r.Slug)
		}
	}
	if len(refs) == 0 {
		t.Error("got zero refs, want at least 1")
	}
}

func TestRelatedLinks(t *testing.T) {
	html := `<!DOCTYPE html><html><head></head><body>
<a href="/dsa/quick-sort/">Quick Sort</a>
<a href="/dsa/heap-sort/">Heap Sort</a>
<a href="https://external.com/other">External</a>
</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	defer srv.Close()

	c := gfg.NewClient()
	c.BaseURL = srv.URL
	c.Rate = 0
	c.Retries = 0

	refs, err := c.RelatedArticles(context.Background(), "dsa/merge-sort", 0)
	if err != nil {
		t.Fatalf("RelatedArticles: %v", err)
	}
	for _, r := range refs {
		if strings.HasPrefix(r.URL, "https://external.com") {
			t.Errorf("Got external ref: %s", r.URL)
		}
	}
	if len(refs) == 0 {
		t.Error("got zero refs, want at least 1")
	}
}

func TestSuggestBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("Forbidden"))
	}))
	defer srv.Close()

	c := gfg.NewClient()
	c.BaseURL = srv.URL
	c.Rate = 0
	c.Retries = 0

	_, err := c.Suggest(context.Background(), "binary", 5)
	if err == nil {
		t.Fatal("Suggest returned nil error on 403")
	}
}

func TestTopicsOffline(t *testing.T) {
	topics := gfg.Topics()
	if len(topics) == 0 {
		t.Fatal("Topics() returned empty list")
	}
	for _, tp := range topics {
		if tp.Slug == "" {
			t.Error("topic has empty slug")
		}
		if tp.Name == "" {
			t.Error("topic has empty name")
		}
		if !strings.HasPrefix(tp.URL, "https://") {
			t.Errorf("topic URL %q does not start with https://", tp.URL)
		}
	}
}

func TestNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := gfg.NewClient()
	c.BaseURL = srv.URL
	c.Rate = 0
	c.Retries = 0

	_, err := c.GetArticle(context.Background(), "dsa/no-such-article")
	if err == nil {
		t.Fatal("GetArticle returned nil error on 404")
	}
}

func TestContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := gfg.NewClient()
	c.BaseURL = srv.URL
	c.Rate = 0
	c.Retries = 0
	c.HTTP.Timeout = 5 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.GetArticle(ctx, "dsa/binary-search")
	if err == nil {
		t.Fatal("GetArticle with cancelled context returned nil error")
	}
}
