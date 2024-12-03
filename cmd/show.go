package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/fatih/color"
	"github.com/google/go-github/v59/github"
	"github.com/spf13/cobra"
)

var numWorkers = 30

// showCmd represents the show command
var showCmd = &cobra.Command{
	Use:   "show",
	Short: "Show CI dashboard",
	RunE: func(cmd *cobra.Command, args []string) error {
		debug, err := cmd.Flags().GetBool("debug")
		if err != nil {
			return err
		}
		if debug {
			slog.SetLogLoggerLevel(slog.LevelDebug)
		}
		if len(args) != 2 {
			cmd.Usage()
			os.Exit(1)
		}
		token := os.Getenv("GITHUB_TOKEN")
		if token == "" {
			slog.Error("Set GITHUB_TOKEN environment variable")
			os.Exit(1)
		}
		client := github.NewClient(nil).WithAuthToken(token)
		owner := args[0]
		repo := args[1]
		ctx := context.Background()
		branch, err := cmd.Flags().GetString("branch")
		if err != nil {
			return err
		}
		numRuns, err := cmd.Flags().GetInt("number")
		if err != nil {
			return err
		}
		workflowFlag, err := cmd.Flags().GetString("workflow")
		if err != nil {
			return err
		}
		event, err := cmd.Flags().GetString("event")
		if err != nil {
			return err
		}
		summary, err := cmd.Flags().GetBool("summary")
		if err != nil {
			return err
		}
		top, err := cmd.Flags().GetInt("top")
		if err != nil {
			return err
		}
		days, err := cmd.Flags().GetInt("days")
		if err != nil {
			return err
		}
		created := daysToTimeRange(days)
		var workflows []string
		details := false
		if workflowFlag != "" {
			workflows = append(workflows, workflowFlag)
			details = true
		} else {
			wf, err := getWorkflows(ctx, client, owner, repo)
			if err != nil {
				return err
			}
			workflows = append(workflows, wf...)
		}
		tasks := make(chan string)
		result := map[string][]*github.WorkflowRun{}
		wg := sync.WaitGroup{}
		mux := sync.Mutex{}
		for i := 0; i < numWorkers; i++ {
			wg.Add(1)
			go func() {
				for workflow := range tasks {
					runs, err := getWorkflowRuns(ctx, client, owner, repo, branch, workflow, event, numRuns, created)
					if err != nil {
						slog.Error("Failed to get workflow runs", slog.Any("error", err))
						continue
					}
					mux.Lock()
					result[workflow] = runs
					mux.Unlock()
				}
				wg.Done()
			}()

		}
		for _, workflow := range workflows {
			tasks <- workflow
		}
		close(tasks)
		wg.Wait()
		if summary {
			printSummary(owner, repo, branch, event, result, top)

		} else {
			for workflow, runs := range result {
				printDashboard(owner, repo, branch, workflow, event, runs)
				if details {
					printDetailedDashboard(ctx, client, owner, repo, runs)
				}
			}
		}
		return nil
	},
}

func daysToTimeRange(days int) string {
	now := time.Now()
	d := time.Duration(days) * 24 * time.Hour
	from := now.Add(-d)
	return fmt.Sprintf(">=%s", from.Format(time.RFC3339))
}

type workflowStats struct {
	workflow        string
	from            string
	to              string
	averageDuration time.Duration
	successRate     float32
	success         int
	count           int
}

func printSummary(owner, repo, branch, event string, result map[string][]*github.WorkflowRun, top int) {
	var statsList []workflowStats
	for workflow, runs := range result {
		if len(runs) == 0 {
			continue
		}
		count := len(runs)
		from := runs[count-1].GetRunStartedAt().Format(time.DateOnly)
		to := runs[0].GetRunStartedAt().Format(time.DateOnly)
		success := 0
		var totalSeconds float64
		for i := 0; i < count; i++ {
			if runs[i].GetConclusion() == "success" {
				success++
				totalSeconds += runs[i].GetUpdatedAt().Time.Sub(runs[i].GetRunStartedAt().Time).Seconds()
			}
		}
		stats := workflowStats{
			workflow:        workflow,
			from:            from,
			to:              to,
			averageDuration: time.Second * time.Duration(totalSeconds/float64(success)),
			successRate:     100 * float32(success) / float32(count),
			success:         success,
			count:           count,
		}
		statsList = append(statsList, stats)
	}
	slices.SortFunc(statsList, func(a, b workflowStats) int {
		return int(a.successRate - b.successRate)
	})
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', 0)
	fmt.Fprintln(w, "from\tto\tsuccess rate\tworkflow")
	for i, stats := range statsList {
		if i >= top {
			break
		}
		link := color.New(color.FgCyan, color.Bold).SprintFunc()
		workflowURL := fmt.Sprintf("https://github.com/%s/%s/actions/workflows/%s?query=branch%%3A%s+event%%3A%s++",
			owner, repo, stats.workflow, branch, event)
		status := fmt.Sprintf("%0.f%%", stats.successRate)
		fmt.Fprintln(w, fmt.Sprintf("%s\t%s\t%s %d/%d\t%s",
			stats.from, stats.to, status, stats.success, stats.count, link(getLink(workflowURL, stats.workflow)),
		))
	}
	w.Flush()
	slices.SortFunc(statsList, func(a, b workflowStats) int {
		return int(b.averageDuration - a.averageDuration)
	})
	fmt.Fprintln(w, "from\tto\taverage duration\tworkflow")
	for i, stats := range statsList {
		if i >= top {
			break
		}
		link := color.New(color.FgCyan, color.Bold).SprintFunc()
		workflowURL := fmt.Sprintf("https://github.com/%s/%s/actions/workflows/%s?query=branch%%3A%s+event%%3A%s++",
			owner, repo, stats.workflow, branch, event)
		fmt.Fprintln(w, fmt.Sprintf("%s\t%s\t%s %d/%d\t%s",
			stats.from, stats.to, stats.averageDuration, stats.success, stats.count, link(getLink(workflowURL, stats.workflow)),
		))
	}
	w.Flush()
}

func getLink(url, text string) string {
	return fmt.Sprintf("\033]8;;%s\033\\%s\033]8;;\033\\", url, text)
}

func printDashboard(owner, repo, branch, workflow, event string, runs []*github.WorkflowRun) {
	count := min(len(runs), 4)
	bold := color.New(color.Bold).SprintFunc()
	link := color.New(color.FgCyan, color.Underline).SprintFunc()
	fmt.Println(bold(workflow),
		link(fmt.Sprintf("https://github.com/%s/%s/actions/workflows/%s?query=branch%%3A%s+event%%3A%s++",
			owner, repo, workflow, branch, event)))
	if len(runs) == 0 {
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', 0)
	fmt.Fprintln(w, "from\tto\tduration\tsuccess rate\t")
	for ; len(runs) >= count; count *= 2 {
		from := runs[count-1].GetRunStartedAt().Format(time.DateTime)
		to := runs[0].GetRunStartedAt().Format(time.DateTime)
		success := 0
		var totalSeconds float64
		for i := 0; i < count; i++ {
			if runs[i].GetConclusion() == "success" {
				success++
				totalSeconds += runs[i].GetUpdatedAt().Time.Sub(runs[i].GetRunStartedAt().Time).Seconds()
			}
		}
		avgDuration := "N/A"
		if totalSeconds != 0 {
			avgDuration = (time.Second * time.Duration(totalSeconds/float64(success))).String()
		}
		successRate := 100 * float32(success) / float32(count)
		statusColor := color.New(color.FgGreen).SprintFunc()
		emoji := "🥰"
		if successRate < 50 {
			statusColor = color.New(color.FgRed).SprintFunc()
			emoji = "🙀"
		} else if successRate < 80 {
			statusColor = color.New(color.FgYellow).SprintFunc()
			emoji = "🤨"
		}
		status := fmt.Sprintf("%s %0.f%%", emoji, successRate)
		fmt.Fprintln(w, fmt.Sprintf("%s\t%s\t%s\t%s\t%d/%d", from, to, avgDuration, statusColor(status), success, count))
	}
	w.Flush()

}

func printDetailedDashboard(ctx context.Context, client *github.Client, owner, repo string, runs []*github.WorkflowRun) {
	failedJobCount := make(map[string]int)
	failedStepCount := make(map[string]int)
	cancelledStepCount := make(map[string]int)
	var logsURLs []*url.URL
	tasks := make(chan int64)
	wg := sync.WaitGroup{}
	mux := sync.Mutex{}
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			for runID := range tasks {
				jobs, err := getJobs(ctx, client, owner, repo, runID)
				if err != nil {
					slog.Error("Failed to get workflow runs", slog.Any("error", err))
					continue
				}
				for _, job := range jobs {
					if job.GetConclusion() == "failure" {
						logsURL, _, err := client.Actions.GetWorkflowJobLogs(ctx, owner, repo, job.GetID(), 10)
						mux.Lock()
						if err == nil {
							logsURLs = append(logsURLs, logsURL)
						}
						count, ok := failedJobCount[job.GetName()]
						if ok {
							failedJobCount[job.GetName()] = count + 1
						} else {
							failedJobCount[job.GetName()] = 1
						}
						for _, step := range job.Steps {
							if step.GetConclusion() == "failure" {
								count, ok := failedStepCount[step.GetName()]
								if ok {
									failedStepCount[step.GetName()] = count + 1
								} else {
									failedStepCount[step.GetName()] = 1
								}
							} else if step.GetConclusion() == "cancelled" {
								count, ok := cancelledStepCount[step.GetName()]
								if ok {
									cancelledStepCount[step.GetName()] = count + 1
								} else {
									cancelledStepCount[step.GetName()] = 1
								}
							}
						}
						mux.Unlock()
					}
				}
			}
			wg.Done()
		}()
	}
	for _, run := range runs {
		if run.GetConclusion() == "failure" {
			tasks <- run.GetID()
		}
	}
	close(tasks)
	wg.Wait()

	failedJobs := sortMapByValue(failedJobCount)
	failedSteps := sortMapByValue(failedStepCount)
	cancelledSteps := sortMapByValue(cancelledStepCount)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', 0)
	red := color.New(color.FgRed, color.Bold)
	if len(failedJobs) > 0 {
		red.Println("\nfailed jobs")
		fmt.Fprintln(w, "job name\tfailure count")
		for _, count := range failedJobs {
			fmt.Fprintln(w, fmt.Sprintf("%s\t%d", count.Name, count.Count))
		}
		w.Flush()
	}
	if len(failedSteps) > 0 {
		red.Println("\nfailed steps")
		fmt.Fprintln(w, "step name\tfailure count")
		for _, count := range failedSteps {
			fmt.Fprintln(w, fmt.Sprintf("%s\t%d", count.Name, count.Count))
		}
		w.Flush()
	}
	if len(cancelledSteps) > 0 {
		red.Println("\ncancelled steps")
		fmt.Fprintln(w, "step name\tfailure count")
		for _, count := range cancelledSteps {
			fmt.Fprintln(w, fmt.Sprintf("%s\t%d", count.Name, count.Count))
		}
		w.Flush()
	}
	analyzeLogs(logsURLs)
}

type failureCount struct {
	Name  string
	Count int
}

func sortMapByValue(m map[string]int) []failureCount {
	var failureCounts []failureCount
	for name, count := range m {
		failureCounts = append(failureCounts, failureCount{Name: name, Count: count})
	}
	slices.SortFunc(failureCounts, func(a, b failureCount) int {
		return b.Count - a.Count
	})
	return failureCounts
}
func analyzeLogs(logsURLs []*url.URL) {
	failedTestCount := make(map[string]int)
	var errors []string
	tasks := make(chan string)
	wg := sync.WaitGroup{}
	mux := sync.Mutex{}
	var errorURLs []string

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			for logsURL := range tasks {
				resp, err := http.Get(logsURL)
				if err != nil {
					slog.Error("Failed to get logs", slog.String("url", logsURL), slog.Any("error", err))
					continue
				}
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					if err != nil {
						slog.Error("Failed to read response body", slog.String("url", logsURL), slog.Any("error", err))
						continue
					}
				}
				r := regexp.MustCompile(`Test \[(.*)]:`)
				matches := r.FindAllStringSubmatch(string(body), 10000)
				mux.Lock()
				for _, match := range matches {
					if len(match) == 2 {
						count, ok := failedTestCount[match[1]]
						if ok {
							failedTestCount[match[1]] = count + 1
						} else {
							failedTestCount[match[1]] = 1
						}
						if match[1] == "check-log-errors" {
							errorURLs = append(errorURLs, logsURL)

						}
					}
				}
				r = regexp.MustCompile(` level=error.*`)
				matches = r.FindAllStringSubmatch(string(body), 10000)
				for _, match := range matches {
					for _, errorMessage := range match {
						errors = append(errors, errorMessage)
					}
				}
				mux.Unlock()
			}
			wg.Done()
		}()
	}
	for _, logsURL := range logsURLs {
		tasks <- logsURL.String()
	}
	close(tasks)
	wg.Wait()
	failedTests := sortMapByValue(failedTestCount)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', 0)
	red := color.New(color.FgRed, color.Bold)
	if len(failedTests) > 0 {
		red.Println("\nfailed tests")
		fmt.Fprintln(w, "test name\tfailure count")
		for _, count := range failedTests {
			fmt.Fprintln(w, fmt.Sprintf("%s\t%d", count.Name, count.Count))
		}
		w.Flush()
	}
	if len(errors) > 0 {
		errorLogCount := make(map[string]int)
		for _, errorMessage := range errors {
			r := regexp.MustCompile(`msg="([^"]+)"`)
			matches := r.FindStringSubmatch(errorMessage)
			if len(matches) == 2 {
				count, ok := errorLogCount[matches[1]]
				if ok {
					errorLogCount[matches[1]] = count + 1
				} else {
					errorLogCount[matches[1]] = 1
				}
			}
		}
		errorLogs := sortMapByValue(errorLogCount)
		red.Println("\nerror logs")
		fmt.Fprintln(w, "error message\tcount")
		for _, count := range errorLogs {
			fmt.Fprintln(w, fmt.Sprintf("%s\t%d", count.Name, count.Count))
		}
		w.Flush()
	}
	for _, errorLogsURL := range errorURLs {
		slog.Debug("Jobs log URL with check-log-errors test failure", slog.String("logs-url", errorLogsURL))
	}
}

func init() {
	rootCmd.AddCommand(showCmd)

	showCmd.Flags().StringP("branch", "b", "main", "Branch name")
	showCmd.Flags().StringP("event", "e", "schedule", "Event type that triggered the workflows")
	showCmd.Flags().BoolP("debug", "d", false, "Print debug logs")
	showCmd.Flags().IntP("number", "n", 64, "The number of workflow runs to process")
	showCmd.Flags().StringP("workflow", "w", "", "Workflow name (e.g. aks-byocni.yaml)")
	showCmd.Flags().BoolP("summary", "s", false, "Print summary")
	showCmd.Flags().IntP("top", "t", 10, "Print top n. Use with --summary flag")
	showCmd.Flags().Int("days", 30, "Limit workflow runs by the number of days")
}
