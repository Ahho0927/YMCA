package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"ymca/internal/lms"
)

type courseQueryFunc func(*lms.Client, string, *cobra.Command, []string) (any, error)

func (a *App) selectedCourses(c *lms.Client, selector string) ([]string, error) {
	if selector != "*" {
		id, err := c.ResolveCourse(selector, func(s string) { fmt.Fprintln(a.err, safeText(s)) })
		if err != nil {
			return nil, err
		}
		return []string{id}, nil
	}
	d, err := c.Dashboard()
	if err != nil {
		return nil, err
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, course := range d.Courses {
		if !lms.ValidCourseSelector(course.ID) {
			return nil, lms.ErrStructure
		}
		if !seen[course.ID] {
			seen[course.ID] = true
			ids = append(ids, course.ID)
		}
	}
	return ids, nil
}

// Successful single-target JSON remains unchanged. Wildcard queries flatten
// into an array. Completed results survive errors, which go to stderr with a
// nonzero exit status. Authentication loss/cancellation stop subsequent work.
func (a *App) courseQuery(cmd *cobra.Command, args []string, selector string, fn courseQueryFunc, emit func(any) error) error {
	batch := selector == "*" || cmd.Parent().Name() == "*"
	var result any
	err := a.withClient(cmd.Context(), func(c *lms.Client) error {
		ids, e := a.selectedCourses(c, selector)
		if e != nil {
			return e
		}
		if selector != "*" {
			result, e = fn(c, ids[0], cmd, args)
			return e
		}
		result, e = collectQueries(cmd.Context(), ids, true, func(id string) (any, error) { return fn(c, id, cmd, args) })
		return e
	})
	if result != nil && (err == nil || batch) {
		if batch {
			result = appendQueryResults([]any{}, result)
		}
		if e := emit(result); e != nil {
			return e
		}
	}
	return err
}

func collectQueries(ctx context.Context, ids []string, skipMissing bool, fn func(string) (any, error)) ([]any, error) {
	out := []any{}
	failures := []error{}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if ctx.Err() != nil {
			return out, errors.Join(append(failures, ctx.Err())...)
		}
		value, err := fn(id)
		if skipMissing && errors.Is(err, lms.ErrNotFound) {
			continue
		}
		out = appendQueryResults(out, value)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", id, err))
			if errors.Is(err, lms.ErrAuth) || ctx.Err() != nil {
				break
			}
		}
	}
	return out, errors.Join(failures...)
}

func appendQueryResults(out []any, value any) []any {
	switch v := value.(type) {
	case nil:
	case *lms.CourseDetail:
		if v != nil {
			out = append(out, v)
		}
	case *lms.MaterialDetail:
		if v != nil {
			out = append(out, v)
		}
	case *lms.Assignment:
		if v != nil {
			out = append(out, v)
		}
	case *lms.NoticeDetail:
		if v != nil {
			out = append(out, v)
		}
	case *lms.Syllabus:
		if v != nil {
			out = append(out, v)
		}
	case []any:
		out = append(out, v...)
	case []lms.Material:
		for _, item := range v {
			out = append(out, item)
		}
	case []lms.Assignment:
		for _, item := range v {
			out = append(out, item)
		}
	default:
		out = append(out, value)
	}
	return out
}

func (a *App) showMaterials(cmd *cobra.Command, c *lms.Client, course string) (any, error) {
	selector := cmd.Parent().Name()
	if selector != "*" {
		return c.Material(selector, course)
	}
	materials, err := c.Materials(course)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	byID := map[string]lms.Material{}
	for _, m := range materials {
		ids = append(ids, m.ID)
		byID[m.ID] = m
	}
	return collectQueries(cmd.Context(), ids, false, func(id string) (any, error) { return c.ReadMaterial(byID[id]) })
}
