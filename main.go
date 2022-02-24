package main

import (
	"flag"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	text_template "text/template"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gorilla/feeds"
	"gopkg.in/yaml.v3"
)

type FeedSpec struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Link        string `yaml:"link"`
	Format      string `yaml:"format"`
	Spec        struct {
		Item        string            `yaml:"item"`
		Values      map[string]string `yaml:"values"`
		Title       string            `yaml:"title"`
		Description string            `yaml:"description"`
		Link        string            `yaml:"link"`
		Filter      string            `yaml:"filter"`
		Date        string            `yaml:"date"`
		DateFormat  string            `yaml:"date_format"`
	} `yaml:"spec"`
}

type Config struct {
	Listen string              `yaml:"listen"`
	Feeds  map[string]FeedSpec `yaml:"feeds"`
}

var (
	configPath = flag.String("config", "", "path to config file")
	config     = Config{Listen: "127.0.0.1:9977"}
)

func handleHome(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "text/html; charset=utf8")
	t := template.Must(template.New("home").Parse(`<!doctype html>
<html>
<h2>Feeds</h2>
<ul>
{{range $slug, $feed := .}}
<li><a href="/feeds/{{$slug}}">{{$feed.Title}}</a></li>
{{end}}
</ul>
</html>
`))
	t.Execute(w, config.Feeds)
}

func handleFeeds(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/feeds/"):]
	feedSpec, ok := config.Feeds[id]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	res, err := http.Get(feedSpec.Link)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		log.Printf("error fetching feed %s: %s", id, err)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		w.WriteHeader(http.StatusBadGateway)
		log.Printf("bad status code fetching feed %s: %d", id, res.StatusCode)
		return
	}

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("error parsing the feed %s: %s", err)
		return
	}

	feed := &feeds.Feed{
		Title:       feedSpec.Title,
		Link:        &feeds.Link{Href: feedSpec.Link},
		Description: feedSpec.Description,
		Items:       []*feeds.Item{},
	}

	format := strings.ToLower(feedSpec.Format)
	if format == "" {
		format = "atom"
	}

	base, err := url.Parse(feedSpec.Link)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("error parsing the feed %s: %s", err)
		return
	}

	titleTmpl := text_template.Must(text_template.New("title").Parse(feedSpec.Spec.Title))
	descTmpl := text_template.Must(text_template.New("desc").Parse(feedSpec.Spec.Description))
	filterTmpl := text_template.Must(text_template.New("filter").Parse(feedSpec.Spec.Filter))

	latestDate := time.Time{}
	doc.Find(feedSpec.Spec.Item).Each(func(i int, s *goquery.Selection) {
		values := make(map[string]*goquery.Selection)
		for name, selector := range feedSpec.Spec.Values {
			values[name] = s.Find(selector)
		}

		var link string

		if feedSpec.Spec.Filter != "" {
			var filterBuilder strings.Builder
			_ = filterTmpl.Execute(&filterBuilder, values)
			filter := filterBuilder.String()

			if filter == "false" || filter == "" {
				return
			}
		}

		var titleBuilder strings.Builder
		_ = titleTmpl.Execute(&titleBuilder, values)
		title := titleBuilder.String()

		var descBuilder strings.Builder
		_ = descTmpl.Execute(&descBuilder, values)
		desc := descBuilder.String()

		u, err := base.Parse(values[feedSpec.Spec.Link].AttrOr("href", ""))
		if err != nil {
			link = "failed to parse link: err"
		} else {
			link = u.String()
		}

		date := time.Time{}
		if feedSpec.Spec.Date != "" {
			format := time.RFC3339
			if feedSpec.Spec.DateFormat != "" {
				format = feedSpec.Spec.DateFormat
			}
			unparsed := strings.TrimSpace(values[feedSpec.Spec.Date].Text())
			if unparsed != "" {
				parsed, err := time.Parse(format, unparsed)
				if err == nil {
					date = parsed
				}
			}
		}
		if date.After(latestDate) {
			latestDate = date
		}

		feed.Items = append(feed.Items, &feeds.Item{
			Title:       title,
			Link:        &feeds.Link{Href: link},
			Description: desc,
			Created:     date,
		})
	})

	if !latestDate.IsZero() {
		feed.Created = latestDate
		w.Header().Add("Date", latestDate.Format(http.TimeFormat))
	}

	switch format {
	case "atom":
		w.Header().Add("Content-Type", "application/atom+xml; charset=UTF-8")
		feed.WriteAtom(w)
	case "rss":
		w.Header().Add("Content-Type", "application/rss+xml; charset=UTF-8")
		feed.WriteRss(w)
	case "json":
		w.Header().Add("Content-Type", "application/feed+json; charset=UTF-8")
		feed.WriteJSON(w)
	}
}

func main() {
	log.SetFlags(0)
	flag.Parse()
	if *configPath != "" {
		var f *os.File
		var err error
		if *configPath == "-" {
			f = os.Stdin
		} else {
			f, err = os.Open(*configPath)
		}
		if err != nil {
			log.Fatalf("failed to open %s: %s", *configPath, err)
		}
		y := yaml.NewDecoder(f)
		if err = y.Decode(&config); err != nil {
			log.Fatalf("failed to load config %s: %s", *configPath, err)
		}
		f.Close()
	}
	log.Printf("listening on http://%s/", config.Listen)
	http.HandleFunc("/", handleHome)
	http.HandleFunc("/feeds/", handleFeeds)
	log.Fatal(http.ListenAndServe(config.Listen, nil))
}
