package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"ymca/internal/lms"
)

type downloadSelection struct {
	material string
	file     string
	lesson   string
	output   string
}

// Assign names from the whole current course list, even for a single-course
// download, so duplicate titles keep the same folders across selection scopes.
func courseFolderNames(courses []lms.Course) map[string]string {
	ordered := append([]lms.Course(nil), courses...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	names := map[string]string{}
	used := map[string]bool{}
	for _, course := range ordered {
		if _, exists := names[course.ID]; exists {
			continue
		}
		base := lms.CourseFolder(course)
		name := base
		for n := 1; used[strings.ToLower(name)]; n++ {
			name = fmt.Sprintf("%s (%d)", base, n)
		}
		used[strings.ToLower(name)] = true
		names[course.ID] = name
	}
	return names
}

func (a *App) downloadCourses(c *lms.Client, selector string, grouped bool) ([]string, map[string]string, error) {
	if !grouped {
		ids, err := a.selectedCourses(c, selector)
		return ids, nil, err
	}
	d, err := c.Dashboard()
	if err != nil {
		return nil, nil, err
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, course := range d.Courses {
		if !lms.ValidCourseSelector(course.ID) {
			return nil, nil, lms.ErrStructure
		}
		if !seen[course.ID] {
			seen[course.ID] = true
			ids = append(ids, course.ID)
		}
	}
	if selector != "*" {
		id, err := c.ResolveCourse(selector, func(s string) { fmt.Fprintln(a.err, safeText(s)) })
		if err != nil {
			return nil, nil, err
		}
		if !seen[id] {
			return nil, nil, errors.New("course was not found in your current LMS course list")
		}
		ids = []string{id}
	}
	return ids, courseFolderNames(d.Courses), nil
}

func selectDownload(cmd *cobra.Command, args []string, hasMaterial bool) (downloadSelection, error) {
	selection := downloadSelection{file: "*"}
	if !hasMaterial && len(args) != 0 {
		return selection, usageError{errors.New("the material ID comes before the action; use 'materials <material-id|*> download [file-id|*]' or 'materials download --all [--lesson <name>]'")}
	}
	if len(args) > 1 {
		return selection, usageError{errors.New("download accepts at most one file ID or quoted '*'; quote '*' to prevent shell filename expansion")}
	}
	if hasMaterial {
		selection.material = cmd.Parent().Name()
	}
	selection.output, _ = cmd.Flags().GetString("output")
	all, _ := cmd.Flags().GetBool("all")
	legacyFile, _ := cmd.Flags().GetString("file")
	var err error
	selection.lesson, err = lessonFlag(cmd)
	if err != nil {
		return selection, err
	}
	if selection.output == "" || (!hasMaterial && !all) || (selection.lesson != "" && hasMaterial && selection.material != "*") {
		return selection, usageError{errors.New("use 'materials <material-id|*> download [file-id|*]' or 'materials download --all [--lesson <name>]'; --output must not be empty")}
	}
	if cmd.Flags().Changed("file") {
		if len(args) != 0 || all || legacyFile == "" {
			return selection, usageError{errors.New("choose either a positional file selector or --file; --file cannot be combined with --all")}
		}
		selection.file = legacyFile
	}
	if len(args) == 1 {
		selection.file = args[0]
		if all && selection.file != "*" {
			return selection, usageError{errors.New("a specific file ID cannot be combined with --all")}
		}
	}
	if selection.file != "*" && !resourceIdentifier.MatchString(selection.file) {
		return selection, usageError{errors.New("file selector must be one file ID or the quoted literal '*'; partial glob patterns are not supported")}
	}
	return selection, nil
}

func (a *App) downloadCommand(selector string, hasMaterial bool) *cobra.Command {
	cmd := &cobra.Command{Use: "download", Short: "Download all materials in the course or selected lesson"}
	if hasMaterial {
		cmd.ValidArgsFunction = allIDCompletion
		cmd.Use = "download [file-id|*]"
		cmd.Short = "Download one attachment or all files in this SRY group; quote '*'"
	}
	cmd.Long = "Download attachments to --output. Bulk scopes use course-name/lesson folders, with numeric suffixes for duplicate course names. Unavailable materials/files are skipped with warnings and do not cause a failing exit status. Authentication expiry and cancellation still stop the download. Quote every '*'."
	cmd.Flags().String("output", ".", "Destination directory")
	if hasMaterial {
		cmd.Flags().Bool("all", false, "Compatibility alias for download '*' (also the default)")
		cmd.Flags().String("file", "", "Compatibility alias for positional file ID")
	} else {
		cmd.Flags().Bool("all", false, "Download all materials; prefer materials '*' download")
		cmd.Flags().String("lesson", "", "Select a lesson by exact displayed name")
	}
	cmd.Args = func(cmd *cobra.Command, args []string) error {
		_, err := selectDownload(cmd, args, hasMaterial)
		return err
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		selection, err := selectDownload(cmd, args, hasMaterial)
		if err != nil {
			return err
		}
		rows := []map[string]string{}
		skipped := false
		materialMatches, fileMatches := 0, 0
		record := func(course string, row map[string]string) {
			if selector == "*" {
				row["course_id"] = course
			}
			rows = append(rows, row)
		}
		skip := func(course string, row map[string]string, cause error) error {
			if errors.Is(cause, lms.ErrAuth) || cmd.Context().Err() != nil {
				row["error"] = SafeError(cause)
				record(course, row)
				return cause
			}
			skipped = true
			row["warning"] = SafeError(cause)
			record(course, row)
			where := "course " + course
			if row["id"] != "" {
				where += ", material " + row["id"]
			}
			if row["file_id"] != "" {
				where += ", file " + row["file_id"]
			}
			fmt.Fprintf(a.err, "Warning: %s: %s (skipped)\n", safeText(where), row["warning"])
			return nil
		}
		grouped := selector == "*" || selection.material == "" || selection.material == "*"
		err = a.withClient(cmd.Context(), func(c *lms.Client) error {
			courses, folders, e := a.downloadCourses(c, selector, grouped)
			if e != nil {
				return e
			}
			for _, course := range courses {
				if e = cmd.Context().Err(); e != nil {
					return e
				}
				materials, e := c.MaterialsInLesson(course, selection.lesson)
				if e != nil {
					if selector != "*" {
						return e
					}
					if e = skip(course, map[string]string{}, e); e != nil {
						return e
					}
					continue
				}
				seen := map[string]bool{}
				for _, m := range materials {
					if selection.material != "" && selection.material != "*" && m.ID != selection.material {
						continue
					}
					if seen[m.ID] {
						continue
					}
					seen[m.ID] = true
					materialMatches++
					detail, e := c.ReadMaterial(m)
					if e != nil {
						if e = skip(course, map[string]string{"id": m.ID}, e); e != nil {
							return e
						}
						continue
					}
					if len(detail.Files) == 0 && selection.file == "*" {
						if e = skip(course, map[string]string{"id": m.ID}, errors.New("this material contains no downloadable attachments")); e != nil {
							return e
						}
						continue
					}
					target := selection.output
					if grouped {
						target = filepath.Join(target, folders[course], lms.MaterialFolder(m))
					}
					for _, file := range detail.Files {
						if selection.file != "*" && file.ID != selection.file {
							continue
						}
						fileMatches++
						path, e := c.Download(file, target)
						if e != nil {
							if e = skip(course, map[string]string{"id": m.ID, "file_id": file.ID}, e); e != nil {
								return e
							}
							continue
						}
						record(course, map[string]string{"id": m.ID, "file_id": file.ID, "path": path})
					}
				}
			}
			if !skipped && selection.material != "" && selection.material != "*" && materialMatches == 0 {
				return errors.New("material was not found")
			}
			if !skipped && selection.file != "*" && fileMatches == 0 {
				return errors.New("attachment was not found in the selected materials; use 'materials <material-id> show' to see file IDs")
			}
			return nil
		})
		if len(rows) > 0 || err == nil {
			if e := a.print(rows); e != nil {
				return e
			}
		}
		if err != nil {
			return err
		}
		return nil
	}
	return cmd
}
