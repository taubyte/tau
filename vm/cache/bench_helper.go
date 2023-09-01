package cache

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sync"
)

type GetTester struct {
	Url             string
	PassingResponse *ResponseCheck
	FailingResponse *ResponseCheck
}

type ResponseCheck struct {
	Body []byte
	Code int
}

// Parallel get will perform a get request on the given urls.
// Does not check response code or body, simply checks if the request received a response
func ParallelGet(iterations int, urls ...string) error {
	errChan := make(chan error, 1)

	go func(it int, _urls ...string) {
		var iterationWG sync.WaitGroup
		iterationWG.Add(iterations)
		for i := 0; i < iterations; i++ {
			var wg sync.WaitGroup
			wg.Add(len(urls))
			for _, url := range _urls {
				go func(_wg *sync.WaitGroup, _url string) {
					defer _wg.Done()

					_, err := http.Get(_url)
					if err != nil {
						errChan <- err
						return
					}

				}(&wg, url)

			}
			wg.Wait()
			iterationWG.Done()

		}
		iterationWG.Wait()
		errChan <- nil
	}(iterations, urls...)

	err := <-errChan
	return err
}

// ParallelGetWithBodyCheck is the same as ParallelGet, but will check body for expected response, and failure response if given.
func ParallelGetWithBodyCheck(iterations int, toTest ...GetTester) error {
	errChan := make(chan error, 1)

	go func(it int, _toTest ...GetTester) {
		var iterationWG sync.WaitGroup
		iterationWG.Add(iterations)
		for i := 0; i < iterations; i++ {
			var wg sync.WaitGroup
			wg.Add(len(toTest))
			for _, tester := range _toTest {
				go func(_wg *sync.WaitGroup, _tester GetTester) {
					defer _wg.Done()

					res, err := http.Get(_tester.Url)
					if err != nil {
						errChan <- err
						return
					}

					if passingResponse := _tester.PassingResponse; passingResponse != nil {
						if body := passingResponse.Body; body != nil {
							_body, err := io.ReadAll(res.Body)
							if err != nil {
								errChan <- err
								return
							}

							if !bytes.Equal(body, _body) {
								errChan <- fmt.Errorf("expected body `%s` got `%s`", string(body), string(_body))
								return
							}
						}

						if code := passingResponse.Code; code != 0 && code != res.StatusCode {
							errChan <- fmt.Errorf("response status code `%d` does not match expected status code `%d`", res.StatusCode, passingResponse.Code)
							return
						}
					}

					if failingResponse := _tester.FailingResponse; failingResponse != nil {
						if body := failingResponse.Body; body != nil {
							_body, err := io.ReadAll(res.Body)
							if err != nil {
								errChan <- err
								return
							}

							if bytes.Equal(body, _body) {
								errChan <- fmt.Errorf("response body matches failing response bytes")
								return
							}
						}

						if code := failingResponse.Code; code != 0 && code == res.StatusCode {
							errChan <- fmt.Errorf("response status code `%d` matches set failing status code `%d`", res.StatusCode, failingResponse.Code)
						}
					}
				}(&wg, tester)

			}
			wg.Wait()
			iterationWG.Done()

		}
		iterationWG.Wait()
		errChan <- nil
	}(iterations, toTest...)

	err := <-errChan
	return err
}
