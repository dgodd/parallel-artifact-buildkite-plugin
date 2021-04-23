package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

type Artifact struct {
	DownloadUrl       string `json:"download_url"`
	UploadDestination string `json:"upload_destination"`
	Path              string `json:"path"`
	Filesize          int    `json:"filesize`
	Sha1sum           string `json:"sha1sum`
}

func getNextPage(links []string) string {
	if len(links) == 0 {
		return ""
	}
	m := regexp.MustCompile(`page=(\d+)&per_page=\d+>; rel="next"`).FindStringSubmatch(links[0])
	if len(m) == 2 {
		return m[1]
	} else {
		return ""
	}
}

func main() {
	token := os.Getenv("BUILDKITE_AGENT_ACCESS_TOKEN")
	if token == "" {
		log.Fatal("Expected: BUILDKITE_AGENT_ACCESS_TOKEN")
	}

	buildID := os.Getenv("BUILDKITE_BUILD_NUMBER")
	if buildID == "" {
		log.Fatal("Expected: BUILDKITE_BUILD_NUMBER")
	}

	s3Bucket := os.Getenv("BUILDKITE_PLUGIN_PARALLEL_ARTIFACT_S3_BUCKET")
	if s3Bucket == "" {
		log.Fatal("Expected: BUILDKITE_PLUGIN_PARALLEL_ARTIFACT_S3_BUCKET (Plugin param 's3_bucket')")
	}

	awsRegion := os.Getenv("BUILDKITE_PLUGIN_PARALLEL_ARTIFACT_AWS_REGION")
	if awsRegion == "" {
		log.Fatal("Expected: BUILDKITE_PLUGIN_PARALLEL_ARTIFACT_AWS_REGION (Plugin param 'aws_region')")
	}

	fileGlobs := strings.Split(os.Getenv("BUILDKITE_PLUGIN_PARALLEL_ARTIFACT_PATTERN"), ";")
	if len(fileGlobs) == 0 || (len(fileGlobs) == 1 && fileGlobs[0] == "") {
		log.Fatal("Expected: BUILDKITE_PLUGIN_PARALLEL_ARTIFACT_PATTERN (Plugin param 'pattern')")
	}

	outDir := os.Getenv("BUILDKITE_PLUGIN_PARALLEL_ARTIFACT_OUTDIR")
	if buildID == "" {
		log.Fatal("Expected: BUILDKITE_PLUGIN_PARALLEL_ARTIFACT_OUTDIR (Plugin param 'outdir')")
	}

	sess, _ := session.NewSession(&aws.Config{
		Region:   aws.String(awsRegion),
	})
	downloader := s3manager.NewDownloader(sess)

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if strings.Contains(req.URL.Host, "cloudfront.net") {
			return errors.New("Use S3 lib")
		}
		return nil
	}}

  artifactsURL := fmt.Sprintf(
    "https://api.buildkite.com/v2/organizations/%s/pipelines/%s/builds/%s/artifacts",
    os.Getenv("BUILDKITE_ORGANIZATION_SLUG"),
    os.Getenv("BUILDKITE_PIPELINE_NAME"),
    buildID,
  )
  fmt.Println("ARTIFACT API:", artifactsURL)

	var wg sync.WaitGroup
	nextPage := "1"
	for nextPage != "" {
		req, _ := http.NewRequest("GET", artifactsURL, nil)
		q := req.URL.Query()
		q.Add("per_page", "100")
		q.Add("page", nextPage)
		req.URL.RawQuery = q.Encode()
		req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", token))
		resp, err := client.Do(req)
		if err != nil {
			log.Fatalf("ERROR: %+v", err)
		}
		defer resp.Body.Close()

		nextPage = getNextPage(resp.Header["Link"])

		var artifacts []Artifact
		err = json.NewDecoder(resp.Body).Decode(&artifacts)
		if err != nil {
			log.Fatalf("ERROR: %+v", err)
		}
		for _, artifact := range artifacts {
			artifact := artifact
			if err := os.MkdirAll(filepath.Join(outDir, filepath.Dir(artifact.Path)), 0777); err != nil {
				log.Fatal(err)
			}
			// TODO: Check all file globs
			if m, err := filepath.Match(fileGlobs[0], artifact.Path); err == nil && m { // && artifact.Filesize > 0 {
				wg.Add(1)
				go func(artifiact Artifact, wg *sync.WaitGroup) {
					defer wg.Done()

					f, err := os.Create(filepath.Join(outDir, artifact.Path))
					if err != nil {
						log.Fatal(err)
					}
					defer f.Close()

					req, _ := http.NewRequest("GET", artifact.DownloadUrl, nil)
					req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", token))
					resp, err := client.Do(req)
					if err != nil && len(resp.Header["Location"]) > 0 && strings.Contains(resp.Header["Location"][0], "cloudfront.net") {
						location := resp.Header["Location"][0]
						u, _ := url.Parse(location)

						numBytes, err := downloader.Download(f, &s3.GetObjectInput{
							Bucket: aws.String(s3Bucket),
							Key:    aws.String(u.Path),
						})
						fmt.Println("s3.GetObjectInput", err, numBytes, filepath.Join(outDir, artifact.Path))
					} else if err != nil {
						log.Fatalf("ERROR: %s", err)
					} else {
						defer resp.Body.Close()
						io.Copy(f, resp.Body)
					}
				}(artifact, &wg)
			}
		}
	}
	wg.Wait()
}
