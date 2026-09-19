// An opt-in controller comparison, not a CI timing test. Use isolated app state.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

const rounds = 5

var sequence atomic.Uint64
var runID = rand.Text()

type target struct {
	URL, Identity string
	client        *http.Client
}

type fixtures struct {
	text, png []byte
}

type distribution struct {
	Samples []float64 `json:"samples_ms"`
	P50     float64   `json:"p50_ms"`
	P95     float64   `json:"p95_ms"`
}

type measurement struct {
	Round      int                     `json:"round"`
	Target     string                  `json:"target"`
	Protocol   int                     `json:"protocol"`
	Workloads  map[string]distribution `json:"workloads"`
	Concurrent float64                 `json:"eight_clients_requests_per_second"`
	Error      string                  `json:"error,omitempty"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	prepare := flag.String("prepare", "", "create a new directory containing synthetic fixtures, then exit")
	fixtureDir := flag.String("fixtures", "", "local directory containing large.txt and transfer.png")
	project := flag.String("project", "", "same fixture directory as mounted inside both isolated applications")
	candidate := target{}
	reference := target{}
	flag.StringVar(&candidate.URL, "candidate", "", "candidate application origin")
	flag.StringVar(&reference.URL, "reference", "", "reference application origin")
	flag.StringVar(&candidate.Identity, "candidate-id", "", "exact candidate revision/image or binary SHA256")
	flag.StringVar(&reference.Identity, "reference-id", "", "exact reference revision/image or binary SHA256")
	host := flag.String("host", "", "application host, CPU/RAM limits, image/toolchain and browser-load details")
	unauthenticated := flag.Bool("unauthenticated", false, "explicit legacy comparison only; both apps must have authentication disabled")
	flag.Parse()
	if *prepare != "" {
		return prepareFixtures(*prepare)
	}
	if *fixtureDir == "" || !filepath.IsAbs(*project) || *host == "" || candidate.Identity == "" || reference.Identity == "" {
		return fmt.Errorf("provide -fixtures, absolute -project, -host, -candidate-id and -reference-id; use disposable application state")
	}
	data, err := readFixtures(*fixtureDir)
	if err != nil {
		return err
	}
	for name, endpoint := range map[string]*target{"CANDIDATE": &candidate, "REFERENCE": &reference} {
		if err := endpoint.login(os.Getenv("PIXIE_BENCH_"+name+"_TOKEN"), *unauthenticated); err != nil {
			return fmt.Errorf("%s: %w", strings.ToLower(name), err)
		}
		defer endpoint.client.CloseIdleConnections()
	}
	if candidate.URL == reference.URL {
		return fmt.Errorf("candidate and reference must be separate application origins")
	}
	out := json.NewEncoder(os.Stdout)
	if err := out.Encode(map[string]any{
		"type": "controller-comparison-v1", "run_id": runID, "started": time.Now().UTC(), "host": *host,
		"candidate": candidate.Identity, "reference": reference.Identity,
		"runner":        map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "cpus": runtime.NumCPU(), "go": runtime.Version()},
		"authenticated": !*unauthenticated, "rounds": rounds, "p95_limit_percent": 5,
		"file_sha256": fmt.Sprintf("%x", sha256.Sum256(data.text)), "file_bytes": len(data.text), "png_sha256": fmt.Sprintf("%x", sha256.Sum256(data.png)), "png_bytes": len(data.png),
		"samples_per_round": map[string]int{"png_http": 60, "project_list": 500, "file_1mib": 300}, "warmup_per_workload": 10, "concurrent_clients": 8, "requests_per_client": 100,
		"timing": "RPC encode/write/read/envelope-decode/ACK; HTTP request/read; payload validation excluded",
	}); err != nil {
		return err
	}
	results := map[string][]measurement{}
	for round := 1; round <= rounds; round++ {
		order := []string{"candidate", "reference"}
		if round%2 == 0 {
			slices.Reverse(order)
		}
		for _, name := range order {
			endpoint := candidate
			if name == "reference" {
				endpoint = reference
			}
			result, err := measure(endpoint, *project, data)
			result.Round, result.Target = round, name
			if err != nil {
				result.Error = err.Error()
			}
			if writeErr := out.Encode(result); writeErr != nil {
				return writeErr
			}
			if err != nil {
				return fmt.Errorf("%s round %d: %w", name, round, err)
			}
			results[name] = append(results[name], result)
		}
	}
	pass := true
	for _, workload := range []string{"png_http", "project_list", "file_1mib"} {
		median := func(name string) float64 {
			values := make([]float64, 0, rounds)
			for _, result := range results[name] {
				values = append(values, result.Workloads[workload].P95)
			}
			return percentile(values, 50)
		}
		current, baseline := median("candidate"), median("reference")
		accepted := withinBudget(current, baseline)
		pass = pass && accepted
		if err := out.Encode(map[string]any{"type": "comparison", "workload": workload, "candidate_p95_ms": current, "reference_p95_ms": baseline, "change_percent": (current/baseline - 1) * 100, "within_5_percent": accepted}); err != nil {
			return err
		}
	}
	if !pass {
		return fmt.Errorf("controller p95 exceeds the 5%% limit; retain all rounds and investigate before acceptance")
	}
	return nil
}

func (t *target) login(token string, unauthenticated bool) error {
	parsed, err := url.Parse(t.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("application URL must be an HTTP(S) origin without credentials, path, query or fragment")
	}
	t.URL = parsed.Scheme + "://" + strings.ToLower(parsed.Host)
	jar, _ := cookiejar.New(nil)
	t.client = &http.Client{Jar: jar, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	status, err := t.http(http.MethodGet, "/auth/status", nil)
	if err != nil {
		return err
	}
	var auth struct {
		Enabled       *bool `json:"authenticationEnabled"`
		Authenticated bool  `json:"authenticated"`
	}
	if err := json.Unmarshal(status, &auth); err != nil || auth.Enabled == nil || *auth.Enabled == unauthenticated {
		return fmt.Errorf("authentication mode does not match the requested comparison")
	}
	if unauthenticated {
		return nil
	}
	if token == "" {
		return fmt.Errorf("set PIXIE_BENCH_CANDIDATE_TOKEN and PIXIE_BENCH_REFERENCE_TOKEN in the private environment")
	}
	body, _ := json.Marshal(map[string]string{"token": token})
	if _, err := t.http(http.MethodPost, "/auth/login", body); err != nil {
		return err
	}
	status, err = t.http(http.MethodGet, "/auth/status", nil)
	if err != nil || json.Unmarshal(status, &auth) != nil || !auth.Authenticated {
		return fmt.Errorf("cookie login did not authenticate")
	}
	return nil
}

func (t target) http(method, path string, body []byte) ([]byte, error) {
	request, err := http.NewRequest(method, t.URL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Origin", t.URL)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := t.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 16<<20+1))
	if response.StatusCode != http.StatusOK || len(data) > 16<<20 {
		return nil, fmt.Errorf("%s %s returned status %d or oversized body", method, path, response.StatusCode)
	}
	return data, err
}

func (t target) connect() (*websocket.Conn, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	endpoint := "ws" + strings.TrimPrefix(t.URL, "http") + "/ws?client=perf-" + runID + "-" + strconv.FormatUint(sequence.Add(1), 10)
	connection, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: t.client, HTTPHeader: http.Header{"Origin": {t.URL}}})
	if err != nil {
		return nil, 0, err
	}
	connection.SetReadLimit(8 << 20)
	_, body, err := connection.Read(ctx)
	var welcome struct {
		Channel string `json:"channel"`
		Data    struct {
			Protocol int `json:"protocolVersion"`
		} `json:"data"`
	}
	if err != nil || json.Unmarshal(body, &welcome) != nil || welcome.Channel != "server.welcome" || welcome.Data.Protocol == 0 {
		connection.CloseNow()
		return nil, 0, fmt.Errorf("missing valid WebSocket welcome: %v", err)
	}
	return connection, welcome.Data.Protocol, nil
}

func rpc(connection *websocket.Conn, method string, params any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id := strconv.FormatUint(sequence.Add(1), 10)
	body, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	if err := connection.Write(ctx, websocket.MessageText, body); err != nil {
		return nil, err
	}
	for {
		_, body, err := connection.Read(ctx)
		if err != nil {
			return nil, err
		}
		var response struct {
			ID     string          `json:"id"`
			OK     bool            `json:"ok"`
			Result json.RawMessage `json:"result"`
			Error  string          `json:"error"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, err
		}
		if response.ID != id {
			continue
		}
		if !response.OK {
			return nil, fmt.Errorf("%s: %s", method, response.Error)
		}
		ack, _ := json.Marshal(map[string]any{"ack": []string{id}})
		return response.Result, connection.Write(ctx, websocket.MessageText, ack)
	}
}

func measure(t target, root string, data fixtures) (measurement, error) {
	result := measurement{Workloads: map[string]distribution{}}
	connection, protocol, err := t.connect()
	if err != nil {
		return result, err
	}
	defer connection.CloseNow()
	result.Protocol = protocol
	opened, err := rpc(connection, "project.open", map[string]string{"path": root})
	if err != nil {
		return result, err
	}
	var project struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(opened, &project) != nil || project.ID == "" {
		return result, fmt.Errorf("project was not admitted")
	}
	expectedFile, err := json.Marshal(map[string]string{"content": string(data.text)})
	if err != nil {
		return result, err
	}
	checkProjects := func(body []byte) error {
		var projects []struct {
			ID    string   `json:"id"`
			Roots []string `json:"roots"`
		}
		if json.Unmarshal(body, &projects) != nil || len(projects) != 1 || projects[0].ID != project.ID || !slices.Equal(projects[0].Roots, []string{root}) {
			return fmt.Errorf("project.list must return exactly the isolated fixture project")
		}
		return nil
	}
	// Preserve the reference order and include the ACK in RPC latency, but not validation.
	for _, workload := range []struct {
		name     string
		count    int
		call     func() ([]byte, error)
		validate func([]byte) error
	}{
		{"png_http", 60, func() ([]byte, error) {
			return t.http(http.MethodGet, "/files/"+url.PathEscape(project.ID)+"/transfer.png", nil)
		}, func(body []byte) error {
			if !bytes.Equal(body, data.png) {
				return fmt.Errorf("PNG content mismatch")
			}
			return nil
		}},
		{"project_list", 500, func() ([]byte, error) { return rpc(connection, "project.list", map[string]any{}) }, checkProjects},
		{"file_1mib", 300, func() ([]byte, error) {
			return rpc(connection, "fs.readFile", map[string]string{"projectId": project.ID, "root": root, "path": "large.txt"})
		}, func(body []byte) error {
			if !bytes.Equal(body, expectedFile) {
				return fmt.Errorf("file content mismatch")
			}
			return nil
		}},
	} {
		samples := make([]float64, 0, workload.count)
		for index := -10; index < workload.count; index++ {
			start := time.Now()
			body, err := workload.call()
			elapsed := float64(time.Since(start)) / float64(time.Millisecond)
			if err == nil {
				err = workload.validate(body)
			}
			if err != nil {
				result.Workloads[workload.name] = summarize(samples)
				return result, err
			}
			if index >= 0 {
				samples = append(samples, elapsed)
			}
		}
		result.Workloads[workload.name] = summarize(samples)
	}
	var group sync.WaitGroup
	errors := make(chan error, 8)
	start := time.Now()
	for range 8 {
		group.Go(func() {
			client, _, err := t.connect()
			if err != nil {
				errors <- err
				return
			}
			defer client.CloseNow()
			for range 100 {
				body, err := rpc(client, "project.list", map[string]any{})
				if err == nil {
					err = checkProjects(body)
				}
				if err != nil {
					errors <- err
					return
				}
			}
		})
	}
	group.Wait()
	close(errors)
	for err := range errors {
		return result, err
	}
	result.Concurrent = 800 / time.Since(start).Seconds()
	return result, nil
}

func summarize(samples []float64) distribution {
	return distribution{Samples: samples, P50: percentile(samples, 50), P95: percentile(samples, 95)}
}

func percentile(values []float64, percent int) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	// Keep the retained reference runner's zero-based percentile convention.
	return ordered[min(len(ordered)-1, len(ordered)*percent/100)]
}

func withinBudget(candidate, reference float64) bool {
	return candidate > 0 && reference > 0 && candidate <= reference*1.05
}

func readFixtures(directory string) (fixtures, error) {
	text, err := os.ReadFile(filepath.Join(directory, "large.txt"))
	if err != nil {
		return fixtures{}, err
	}
	if len(text) != 1<<20 && !(len(text) == (1<<20)+1 && text[len(text)-1] == '\n') {
		return fixtures{}, fmt.Errorf("large.txt must contain one MiB of text, optionally followed by the legacy newline")
	}
	for _, value := range text {
		if value > 127 || value == '<' || value == '>' || value == '&' {
			return fixtures{}, fmt.Errorf("use generated ASCII fixtures with identical Go/JavaScript JSON escaping")
		}
	}
	data, err := os.ReadFile(filepath.Join(directory, "transfer.png"))
	if err != nil {
		return fixtures{}, err
	}
	if len(data) > 16<<20 {
		return fixtures{}, fmt.Errorf("PNG exceeds the image limit")
	}
	if _, err := png.DecodeConfig(bytes.NewReader(data)); err != nil {
		return fixtures{}, err
	}
	return fixtures{text: text, png: data}, nil
}

func prepareFixtures(directory string) error {
	// Refuse existing directories so a mistyped path cannot overwrite user files.
	if err := os.Mkdir(directory, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "large.txt"), bytes.Repeat([]byte("0123456789abcdef"), 1<<16), 0644); err != nil {
		return err
	}
	img := image.NewNRGBA(image.Rect(0, 0, 512, 512))
	seed := uint32(1)
	for i := 0; i < len(img.Pix); i += 4 {
		for channel := range 3 {
			seed = seed*1664525 + 1013904223
			img.Pix[i+channel] = byte(seed >> 24)
		}
		img.Pix[i+3] = 255
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, "transfer.png"), encoded.Bytes(), 0644)
}
