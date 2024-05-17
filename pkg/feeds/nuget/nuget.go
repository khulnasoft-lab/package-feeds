package nuget

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/khulnasoft-lab/package-feeds/pkg/feeds"
	"github.com/khulnasoft-lab/package-feeds/pkg/useragent"
	"github.com/khulnasoft-lab/package-feeds/pkg/utils"
)

const (
	FeedName           = "nuget"
	catalogServiceType = "Catalog/3.0.0"
	indexPath          = "/v3/index.json"
)

var (
	httpClient = &http.Client{
		Transport: &useragent.RoundTripper{UserAgent: feeds.DefaultUserAgent},
		Timeout:   10 * time.Second,
	}
	errCatalogService = errors.New("error fetching catalog service")
)

type serviceIndex struct {
	Services []*nugetService `json:"resources"`
}

type nugetService struct {
	URI  string `json:"@id"`
	Type string `json:"@type"`
}

type catalog struct {
	Pages []*catalogPage `json:"items"`
}

type catalogPage struct {
	URI      string         `json:"@id"`
	Created  time.Time      `json:"commitTimeStamp"`
	Packages []*catalogLeaf `json:"items"`
}

type catalogLeaf struct {
	URI            string    `json:"@id"`
	CatalogCreated time.Time `json:"commitTimeStamp"`
	Type           string    `json:"@type"`
}

type nugetPackageDetails struct {
	PackageID string    `json:"id"`
	Version   string    `json:"version"`
	Created   time.Time `json:"published"`
}

func fetchCatalogService(baseURL string) (*nugetService, error) {
	var err error
	catalogServiceURL, err := url.JoinPath(baseURL, indexPath)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Get(catalogServiceURL)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	err = utils.CheckResponseStatus(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch nuget catalog service: %w", err)
	}

	directory := &serviceIndex{}
	err = json.NewDecoder(resp.Body).Decode(directory)
	if err != nil {
		return nil, err
	}

	for _, service := range directory.Services {
		if service.Type == catalogServiceType {
			return service, nil
		}
	}
	return nil, fmt.Errorf("%w : could not locate catalog service for nuget feed %s",
		errCatalogService, catalogServiceURL)
}

func fetchCatalogPages(catalogURL string) ([]*catalogPage, error) {
	resp, err := httpClient.Get(catalogURL)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	err = utils.CheckResponseStatus(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch nuget catalog pages: %w", err)
	}

	c := &catalog{}
	err = json.NewDecoder(resp.Body).Decode(c)
	if err != nil {
		return nil, err
	}

	return c.Pages, nil
}

func fetchCatalogPage(catalogURL string) ([]*catalogLeaf, error) {
	resp, err := httpClient.Get(catalogURL)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	err = utils.CheckResponseStatus(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch nuget catalog page: %w", err)
	}

	page := &catalogPage{}
	err = json.NewDecoder(resp.Body).Decode(page)
	if err != nil {
		return nil, err
	}

	return page.Packages, nil
}

func fetchPackageInfo(infoURL string) (*nugetPackageDetails, error) {
	resp, err := httpClient.Get(infoURL)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	err = utils.CheckResponseStatus(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch nuget package data: %w", err)
	}

	packageDetail := &nugetPackageDetails{}
	err = json.NewDecoder(resp.Body).Decode(packageDetail)
	if err != nil {
		return nil, err
	}

	return packageDetail, nil
}

type Feed struct {
	baseURL string
	options feeds.FeedOptions
}

func New(feedOptions feeds.FeedOptions) (*Feed, error) {
	if feedOptions.Packages != nil {
		return nil, feeds.UnsupportedOptionError{
			Feed:   FeedName,
			Option: "packages",
		}
	}
	return &Feed{
		baseURL: "https://api.nuget.org/",
		options: feedOptions,
	}, nil
}

// Latest will parse all creation events for packages in the nuget.org catalog feed
// for packages that have been published since the cutoff
// https://docs.microsoft.com/en-us/nuget/api/catalog-resource
func (feed Feed) Latest(cutoff time.Time) ([]*feeds.Package, time.Time, []error) {
	pkgs := []*feeds.Package{}
	var errs []error

	catalogService, err := fetchCatalogService(feed.baseURL)
	if err != nil {
		return nil, cutoff, append(errs, err)
	}

	catalogPages, err := fetchCatalogPages(catalogService.URI)
	if err != nil {
		return nil, cutoff, append(errs, err)
	}

	for _, catalogPage := range catalogPages {
		if catalogPage.Created.Before(cutoff) {
			continue
		}

		page, err := fetchCatalogPage(catalogPage.URI)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		for _, catalogLeafNode := range page {
			if catalogLeafNode.CatalogCreated.Before(cutoff) {
				continue
			}

			if catalogLeafNode.Type != "nuget:PackageDetails" {
				continue // Not currently interested in package deletion events
			}

			pkgInfo, err := fetchPackageInfo(catalogLeafNode.URI)
			if err != nil {
				errs = append(errs, err)
				continue
			}

			pkg := feeds.NewPackage(pkgInfo.Created, pkgInfo.PackageID, pkgInfo.Version, FeedName)
			pkgs = append(pkgs, pkg)
		}
	}
	newCutoff := feeds.FindCutoff(cutoff, pkgs)
	pkgs = feeds.ApplyCutoff(pkgs, cutoff)

	return pkgs, newCutoff, errs
}

func (feed Feed) GetName() string {
	return FeedName
}

func (feed Feed) GetFeedOptions() feeds.FeedOptions {
	return feed.options
}
