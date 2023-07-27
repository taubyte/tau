package helpers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"text/template"

	"github.com/taubyte/go-interfaces/services/patrick"
	httpAuthClient "github.com/taubyte/tau/clients/http/auth"
	commonAuth "github.com/taubyte/tau/protocols/common"
)

var ac *httpAuthClient.Client

func RegisterTestProject(ctx context.Context, mockAuthURL string) (err error) {
	// override ID of project generated so that it matches id in config
	commonAuth.GetNewProjectID = func(args ...interface{}) string { return ProjectID }

	ac, err = httpAuthClient.New(ctx, httpAuthClient.URL(mockAuthURL), httpAuthClient.Auth(GitToken), httpAuthClient.Provider("github"), httpAuthClient.Unsecure())
	if err != nil {
		return err
	}
	// Generate config, code repositories
	err = RegisterTestRepositories(ctx, "", ConfigRepo, CodeRepo)
	if err != nil {
		return err
	}

	// Register project (
	p := httpAuthClient.Project{Name: ProjectName}

	// Register project with auth
	err = p.Create(ac, fmt.Sprintf("%d", ConfigRepo.ID), fmt.Sprintf("%d", CodeRepo.ID))
	if err != nil {
		return err
	}

	return nil
}

func RegisterTestDomain(ctx context.Context, mockAuthURL string) (err error) {
	ac, err = httpAuthClient.New(ctx, httpAuthClient.URL(mockAuthURL), httpAuthClient.Auth(GitToken), httpAuthClient.Provider("github"), httpAuthClient.Unsecure())
	if err != nil {
		return err
	}

	_, err = ac.RegisterDomain(TestFQDN, ProjectID)

	return err
}

func RegisterTestRepositories(ctx context.Context, mockAuthURL string, repos ...Repository) (err error) {
	if len(mockAuthURL) != 0 {
		ac, err = httpAuthClient.New(ctx, httpAuthClient.URL(mockAuthURL), httpAuthClient.Auth(GitToken), httpAuthClient.Provider("github"), httpAuthClient.Unsecure())
		if err != nil {
			return err
		}
	}

	for _, repo := range repos {
		err = ac.RegisterRepository(fmt.Sprintf("%d", repo.ID))
		if err != nil {
			break
		}
	}

	return err
}

func createStruct(payload []byte) (patrick.Meta, error) {
	var newMeta patrick.Meta

	// Unmarshal the needed json fields into the structure
	err := json.Unmarshal(payload, &newMeta)
	if err != nil {
		return patrick.Meta{}, fmt.Errorf("failed unmarshalling payload into struct with error: %w", err)
	}

	return newMeta, nil
}

func PushJob(gitPayload []byte, patrickURL string, repo Repository) error {
	client := CreateHttpClient()
	url := fmt.Sprintf("%s/github/%s", patrickURL, ProjectID)
	req, err := http.NewRequest("POST", url, bytes.NewReader(gitPayload))
	if err != nil {
		return fmt.Errorf("failed new request with error: %w", err)
	}

	// Add all headers
	req.Header.Add("Accept", "*/*")
	req.Header.Add("content-type", "application/json")
	req.Header.Add("User-Agent", "GitHub-Hookshot/ae56f53")
	req.Header.Add("X-GitHub-Delivery", "048aff50-b519-11ec-98cc-4f6fcbcf321f")
	req.Header.Add("X-GitHub-Event", "push")
	req.Header.Add("X-GitHub-Hook-ID", "350909926")
	req.Header.Add("X-GitHub-Hook-Installation-Target-ID", fmt.Sprintf("%d", repo.ID)) // repoID
	req.Header.Add("X-GitHub-Hook-Installation-Target-Type", "repository")
	req.Header.Add("X-Hub-Signature", "sha1="+generateHMAC(gitPayload, "taubyte_secret"))
	req.Header.Add("X-Hub-Signature-256", "sha256=")

	_, err = client.Do(req)
	if err != nil {
		return fmt.Errorf("failed client Do with error: %w", err)
	}
	return nil
}

func CreateHttpClient() *http.Client {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &http.Client{Transport: tr}
}

func generateHMAC(body []byte, secret string) string {
	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func MakeTemplate(id int, fullname string, branch string) ([]byte, error) {
	if len(branch) == 0 {
		branch = "master"
	}
	splitName := strings.Split(fullname, "/")
	if len(splitName) != 2 {
		return nil, fmt.Errorf("expected fullname to be `username/repo-name` got `%s`", fullname)
	}
	type repo struct {
		ID                     int
		Name, RepoName, Branch string
	}
	var repoInfo = &repo{ID: id, Name: splitName[0], RepoName: splitName[1], Branch: branch}

	t := template.Must(template.New("repoInformation").Parse(string(TemplatePayload)))

	var reader bytes.Buffer
	err := t.Execute(&reader, repoInfo)
	if err != nil {
		log.Println("executing template:", err)
	}
	return reader.Bytes(), nil
}
