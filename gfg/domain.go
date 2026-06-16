// Package gfg exposes GeeksforGeeks as a kit Domain so a blank import in a
// multi-domain host enables the driver:
//
//	import _ "github.com/tamnd/gfg-cli/gfg"
//
// The Domain also builds the standalone gfg binary via cli.NewApp.
package gfg

import (
	"context"
	"errors"
	"strings"

	"github.com/tamnd/any-cli/kit"
	"github.com/tamnd/any-cli/kit/errs"
)

func init() { kit.Register(Domain{}) }

// Domain is the GFG driver. Stateless; the per-run client is built by the
// factory Register passes to kit.
type Domain struct{}

// Info describes the scheme and the hostnames a pasted link is matched against.
func (Domain) Info() kit.DomainInfo {
	return kit.DomainInfo{
		Scheme: "gfg",
		Hosts:  []string{Host, "geeksforgeeks.org"},
		Identity: kit.Identity{
			Binary: "gfg",
			Short:  "Browse GeeksForGeeks tutorials and articles",
			Long: `gfg reads public GeeksforGeeks data the way a browser does:
article detail, related links, category listings, and the well-known topic
index. The article and category surfaces work from any IP; the search page
is pure client-side JavaScript so gfg suggest is the alternative. No API
key, no login, nothing extra to run.

gfg is an independent tool and is not affiliated with GeeksforGeeks.`,
			Site: "https://" + Host,
			Repo: "https://github.com/tamnd/gfg-cli",
		},
	}
}

// Register installs the client factory and all operations.
func (Domain) Register(app *kit.App) {
	app.SetClient(newClient)

	kit.Handle(app, kit.OpMeta{
		Name: "article", Group: "read", Single: true,
		Summary: "Fetch one article by slug or URL",
		URIType: "article", Resolver: true,
		Args: []kit.Arg{{Name: "slug", Help: "article slug or URL"}},
	}, getArticle)

	kit.Handle(app, kit.OpMeta{
		Name: "related", Group: "read", List: true,
		Summary: "List related articles (links extracted from an article page)",
		URIType: "article",
		Args:    []kit.Arg{{Name: "slug", Help: "article slug or URL"}},
	}, getRelated)

	kit.Handle(app, kit.OpMeta{
		Name: "category", Group: "read", List: true,
		Summary: "List articles on a category page",
		URIType: "article",
		Args:    []kit.Arg{{Name: "slug", Help: "category slug or URL"}},
	}, getCategory)

	kit.Handle(app, kit.OpMeta{
		Name:    "suggest",
		Group:   "read",
		List:    true,
		Summary: "Search autocomplete (blocked from datacenter IPs)",
		Args:    []kit.Arg{{Name: "prefix", Help: "search prefix"}},
	}, getSuggest)

	kit.Handle(app, kit.OpMeta{
		Name:    "topics",
		Group:   "read",
		List:    true,
		Summary: "List well-known topic slugs (offline)",
	}, getTopics)
}

// newClient builds a Client from the resolved kit Config.
func newClient(_ context.Context, cfg kit.Config) (any, error) {
	c := NewClient()
	if cfg.UserAgent != "" {
		c.UserAgent = cfg.UserAgent
	}
	if cfg.Rate > 0 {
		c.Rate = cfg.Rate
	}
	if cfg.Retries > 0 {
		c.Retries = cfg.Retries
	}
	if cfg.Timeout > 0 {
		c.HTTP.Timeout = cfg.Timeout
	}
	return c, nil
}

// Defaults seeds the kit baseline with GFG's own defaults.
func Defaults(c *kit.Config) {
	c.Rate = DefaultRate
	c.Retries = DefaultRetries
	c.Timeout = DefaultTimeout
	c.UserAgent = DefaultUserAgent
}

// --- input structs ---

type slugRef struct {
	Slug   string  `kit:"arg" help:"article slug or URL"`
	Client *Client `kit:"inject"`
}

type slugListRef struct {
	Slug   string  `kit:"arg" help:"slug or URL"`
	Limit  int     `kit:"flag,inherit"`
	Client *Client `kit:"inject"`
}

type prefixRef struct {
	Prefix string  `kit:"arg" help:"search prefix"`
	Limit  int     `kit:"flag,inherit"`
	Client *Client `kit:"inject"`
}

type noArgs struct {
	Limit  int     `kit:"flag,inherit"`
	Client *Client `kit:"inject"`
}

// --- handlers ---

func getArticle(ctx context.Context, in slugRef, emit func(*Article) error) error {
	art, err := in.Client.GetArticle(ctx, in.Slug)
	if err != nil {
		return mapErr(err)
	}
	return emit(art)
}

func getRelated(ctx context.Context, in slugListRef, emit func(*ArticleRef) error) error {
	refs, err := in.Client.RelatedArticles(ctx, in.Slug, in.Limit)
	if err != nil {
		return mapErr(err)
	}
	for _, r := range refs {
		if err := emit(r); err != nil {
			return err
		}
	}
	return nil
}

func getCategory(ctx context.Context, in slugListRef, emit func(*ArticleRef) error) error {
	refs, err := in.Client.CategoryArticles(ctx, in.Slug, in.Limit)
	if err != nil {
		return mapErr(err)
	}
	for _, r := range refs {
		if err := emit(r); err != nil {
			return err
		}
	}
	return nil
}

func getSuggest(ctx context.Context, in prefixRef, emit func(*Suggestion) error) error {
	ss, err := in.Client.Suggest(ctx, in.Prefix, in.Limit)
	if err != nil {
		return mapErr(err)
	}
	for _, s := range ss {
		if err := emit(s); err != nil {
			return err
		}
	}
	return nil
}

func getTopics(_ context.Context, in noArgs, emit func(*Topic) error) error {
	topics := Topics()
	limit := in.Limit
	for i, t := range topics {
		if limit > 0 && i >= limit {
			break
		}
		if err := emit(t); err != nil {
			return err
		}
	}
	return nil
}

// --- Resolver ---

// Classify turns any accepted input into the canonical (uriType, id).
func (Domain) Classify(input string) (uriType, id string, err error) {
	slug := normalizeSlug(input)
	if slug == "" {
		return "", "", errs.Usage("unrecognized gfg reference: %q", input)
	}
	return "article", slug, nil
}

// Locate builds the canonical URL for a (uriType, id).
func (Domain) Locate(uriType, id string) (string, error) {
	if uriType != "article" {
		return "", errs.Usage("gfg has no resource type %q", uriType)
	}
	slug := strings.Trim(id, "/")
	if slug == "" {
		return "", errs.Usage("empty id")
	}
	return BaseURL + "/" + slug + "/", nil
}

// mapErr translates library errors into kit exit codes.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrNotFound):
		return errs.NotFound("%s", err.Error())
	case errors.Is(err, ErrRateLimited):
		return errs.RateLimited("%s", err.Error())
	case errors.Is(err, ErrBlocked):
		return errs.NeedAuth("geeksforgeeks.org WAF blocked this request: %s\nHint: gfg suggest and gfg topics work from any IP; this path requires a browser IP", err.Error())
	default:
		return err
	}
}

// Identity returns the CLI identity shared by the domain and the binary.
func Identity() kit.Identity {
	return kit.Identity{
		Binary: "gfg",
		Short:  "Browse GeeksForGeeks tutorials and articles",
		Site:   "https://" + Host,
		Repo:   "https://github.com/tamnd/gfg-cli",
	}
}

