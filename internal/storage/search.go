package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/orvixemail/orvix/internal/models"
)

type SearchService struct {
	index bleve.Index
	path  string
}

type SearchResult struct {
	ID       uint      `json:"id"`
	Subject  string    `json:"subject"`
	FromAddr string    `json:"from_addr"`
	ToAddrs  string    `json:"to_addrs"`
	BodyText string    `json:"body_text"`
	Date     time.Time `json:"date"`
	Score    float64   `json:"score"`
}

type SearchQuery struct {
	Query      string `json:"query"`
	From       string `json:"from,omitempty"`
	To         string `json:"to,omitempty"`
	Subject    string `json:"subject,omitempty"`
	DateFrom   string `json:"date_from,omitempty"`
	DateTo     string `json:"date_to,omitempty"`
	MaxResults int    `json:"max_results"`
	Offset     int    `json:"offset"`
}

func NewSearchService(indexPath string) (*SearchService, error) {
	path := indexPath
	if path == "" {
		path = filepath.Join(os.TempDir(), "orvix-search-index")
	}

	var index bleve.Index
	if _, err := os.Stat(path); err == nil {
		index, err = bleve.Open(path)
		if err != nil {
			return nil, fmt.Errorf("failed to open search index: %w", err)
		}
	} else {
		mapping := bleve.NewIndexMapping()

		docMapping := bleve.NewDocumentMapping()

		subjectMapping := bleve.NewTextFieldMapping()
		subjectMapping.Analyzer = "en"
		docMapping.AddFieldMappingsAt("subject", subjectMapping)

		fromMapping := bleve.NewTextFieldMapping()
		docMapping.AddFieldMappingsAt("from_addr", fromMapping)

		bodyMapping := bleve.NewTextFieldMapping()
		bodyMapping.Analyzer = "en"
		docMapping.AddFieldMappingsAt("body_text", bodyMapping)

		mapping.AddDocumentMapping("message", docMapping)

		index, err = bleve.New(path, mapping)
		if err != nil {
			return nil, fmt.Errorf("failed to create search index: %w", err)
		}
	}

	return &SearchService{index: index, path: path}, nil
}

func (s *SearchService) IndexMessage(msg *models.Message) error {
	data := struct {
		ID       uint      `json:"id"`
		Subject  string    `json:"subject"`
		FromAddr string    `json:"from_addr"`
		ToAddrs  string    `json:"to_addrs"`
		BodyText string    `json:"body_text"`
		Date     time.Time `json:"date"`
	}{
		ID:       msg.ID,
		Subject:  msg.Subject,
		FromAddr: msg.FromAddr,
		ToAddrs:  msg.ToAddrs,
		BodyText: msg.BodyText,
		Date:     msg.Date,
	}
	return s.index.Index(fmt.Sprintf("%d", msg.ID), data)
}

func (s *SearchService) DeleteFromIndex(msgID uint) error {
	return s.index.Delete(fmt.Sprintf("%d", msgID))
}

func (s *SearchService) Search(query SearchQuery) ([]SearchResult, int, error) {
	var searchQuery *bleve.SearchRequest

	if query.MaxResults == 0 {
		query.MaxResults = 20
	}

	if query.Query != "" {
		q := bleve.NewQueryStringQuery(query.Query)
		searchQuery = bleve.NewSearchRequest(q)
	} else {
		// Match all if no query
		q := bleve.NewMatchAllQuery()
		searchQuery = bleve.NewSearchRequest(q)
	}

	searchQuery.Size = query.MaxResults
	searchQuery.From = query.Offset
	searchQuery.SortBy([]string{"-date"})

	result, err := s.index.Search(searchQuery)
	if err != nil {
		return nil, 0, fmt.Errorf("search failed: %w", err)
	}

	var results []SearchResult
	for _, hit := range result.Hits {
		results = append(results, SearchResult{
			Score: hit.Score,
		})
	}

	return results, int(result.Total), nil
}

func (s *SearchService) Close() error {
	return s.index.Close()
}
