package cmd

import (
	"context"
	"path"
	"slices"

	"github.com/google/go-github/v59/github"
)

func getWorkflows(ctx context.Context, client *github.Client, owner, repo string) ([]string, error) {
	listOptions := github.ListOptions{}
	var workflows []*github.Workflow
	for {
		wf, res, err := client.Actions.ListWorkflows(ctx, owner, repo, &listOptions)
		if err != nil {
			return nil, err
		}
		workflows = append(workflows, wf.Workflows...)
		if res.NextPage == 0 {
			break
		}
		listOptions.Page = res.NextPage
	}
	var filepaths []string
	for _, workflow := range workflows {
		filepaths = append(filepaths, path.Base(workflow.GetPath()))
	}
	slices.Sort(filepaths)
	return filepaths, nil
}

func getWorkflowRuns(ctx context.Context, client *github.Client, owner, repo, branch, workflow, event string, count int) ([]*github.WorkflowRun, error) {
	listOptions := github.ListWorkflowRunsOptions{
		Branch:      branch,
		Event:       event,
		ListOptions: github.ListOptions{},
	}
	var workflowRuns []*github.WorkflowRun
	for {
		runs, res, err := client.Actions.ListWorkflowRunsByFileName(ctx, owner, repo, workflow, &listOptions)
		if err != nil {
			return workflowRuns, err
		}
		for _, run := range runs.WorkflowRuns {
			if run.GetConclusion() == "success" || run.GetConclusion() == "failure" {
				workflowRuns = append(workflowRuns, run)
			}
		}
		if res.NextPage == 0 || len(workflowRuns) >= count {
			break
		}
		listOptions.Page = res.NextPage
	}
	if len(workflowRuns) > count {
		return workflowRuns[:count], nil
	}
	return workflowRuns, nil
}

func getJobs(ctx context.Context, client *github.Client, owner, repo string, runID int64) ([]*github.WorkflowJob, error) {
	listOptions := github.ListWorkflowJobsOptions{
		ListOptions: github.ListOptions{},
	}
	var result []*github.WorkflowJob
	for {
		jobs, res, err := client.Actions.ListWorkflowJobs(ctx, owner, repo, runID, &listOptions)
		if err != nil {
			return result, err
		}
		result = append(result, jobs.Jobs...)
		if res.NextPage == 0 {
			break
		}
		listOptions.Page = res.NextPage
	}
	return result, nil
}
