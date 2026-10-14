package checker

import (
	"context"
	"sync"
)

type indexedURL struct {
	index int
	url   string
}

type indexedResult struct {
	index  int
	result CheckResult
}

// CheckBatch processes a slice of URLs concurrently with bounded workers,
// returning results strictly in the order of the input URLs.
func (c *Checker) CheckBatch(ctx context.Context, urls []string, concurrency int) []CheckResult {
	if len(urls) == 0 {
		return nil
	}

	if concurrency <= 0 {
		concurrency = 5
	}
	if concurrency > len(urls) {
		concurrency = len(urls)
	}

	jobs := make(chan indexedURL, len(urls))
	resultsChan := make(chan indexedResult, len(urls))

	for i, u := range urls {
		jobs <- indexedURL{index: i, url: u}
	}
	close(jobs)

	var wg sync.WaitGroup
	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				// If parent context is already canceled, return canceled result
				if ctx.Err() != nil {
					resultsChan <- indexedResult{
						index: job.index,
						result: CheckResult{
							InputURL:  job.url,
							FinalURL:  job.url,
							Status:    StatusNetworkError,
							ErrorCode: new("context_canceled"),
							Warnings:  make([]string, 0),
						},
					}
					continue
				}

				res := c.Check(ctx, job.url)
				resultsChan <- indexedResult{
					index:  job.index,
					result: res,
				}
			}
		}()
	}

	wg.Wait()
	close(resultsChan)

	ordered := make([]CheckResult, len(urls))
	for ir := range resultsChan {
		ordered[ir.index] = ir.result
	}

	return ordered
}
