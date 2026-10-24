package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"ymca/internal/lms"
)

// courses has a positional scope before its static resource/action tree. This
// adapter builds that tree for parsing, help and completion without accessing LMS.
func (a *App) resourceCommands(root *cobra.Command) {
	courses := &cobra.Command{
		Use: "courses <course> <command>", Short: "Read one course by ID/code, or all current courses with quoted '*'",
		Long:               "List courses with 'ymca courses list'. Use 'ymca courses <KOG-ID|lecture-code> materials list', 'reports list', 'show' or 'syllabus'. Quote '*' to select all current courses or resource IDs; partial glob patterns are not supported.",
		DisableFlagParsing: true,
		RunE:               a.routeCourses,
		ValidArgsFunction:  a.completeCourses,
	}
	root.AddCommand(courses)
	root.AddCommand(&cobra.Command{
		Use: "notices [notice-id] <command>", Short: "Read the global LMS notice box",
		DisableFlagParsing: true, RunE: a.routeNotices, ValidArgsFunction: a.completeNotices,
	})
	// Old paths are intentionally not aliases: keeping two public grammars would
	// obscure the course-first design. Fail locally with a concrete migration hint.
	for _, name := range []string{"materials", "assignments"} {
		old := name
		replacement := name
		if name == "assignments" {
			replacement = "reports"
		}
		root.AddCommand(&cobra.Command{Use: old, Hidden: true, DisableFlagParsing: true, RunE: func(_ *cobra.Command, _ []string) error {
			return usageError{fmt.Errorf("'%s' moved; use 'ymca courses <course> %s list' or 'ymca courses <course> %s <id> show'", old, replacement, replacement)}
		}})
	}
}

func (a *App) globalFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().BoolVar(&a.json, "json", a.json, "Print machine-readable JSON")
	cmd.PersistentFlags().StringVar(&a.store.Dir, "state-dir", a.store.Dir, "Private session and worker-state directory")
	cmd.PersistentFlags().StringVar(&a.captureDir, "capture-dir", a.captureDir, "Save private authenticated HTML for parser development (contains personal data)")
}

func (a *App) scopePrefix(args []string) ([]string, bool, error) {
	flags := pflag.NewFlagSet("courses", pflag.ContinueOnError)
	flags.SetOutput(a.err)
	flags.SetInterspersed(false)
	flags.BoolVar(&a.json, "json", a.json, "")
	flags.StringVar(&a.store.Dir, "state-dir", a.store.Dir, "")
	flags.StringVar(&a.captureDir, "capture-dir", a.captureDir, "")
	help := flags.BoolP("help", "h", false, "")
	if err := flags.Parse(args); err != nil {
		return nil, false, usageError{err}
	}
	return flags.Args(), *help, nil
}

func (a *App) routeCourses(cmd *cobra.Command, args []string) error {
	rest, help, err := a.scopePrefix(args)
	if err != nil {
		return err
	}
	selector := "<course>"
	if len(rest) > 0 && rest[0] != "list" {
		selector = rest[0]
		if selector != "*" && !lms.ValidCourseSelector(selector) {
			return usageError{errors.New("use 'ymca courses list' or 'ymca courses <KOG-ID|7-character-code|*> <show|materials|reports|syllabus>'; the course comes before the command; quote '*' for all current courses (no partial glob patterns)")}
		}
	}
	inner := a.courseTree(selector)
	argv := append([]string{"courses"}, rest...)
	if help {
		argv = append(argv, "--help")
	}
	if err := bindResourceScope(inner, argv); err != nil {
		return err
	}
	inner.SetArgs(argv)
	return inner.ExecuteContext(cmd.Context())
}

func showGroupHelp(cmd *cobra.Command, _ []string) error { return cmd.Help() }

func noFileCompletion(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func (a *App) courseTree(selector string) *cobra.Command {
	root := &cobra.Command{Use: "ymca", SilenceErrors: true, SilenceUsage: true}
	root.SetOut(a.out)
	root.SetErr(a.err)
	root.SetFlagErrorFunc(func(_ *cobra.Command, e error) error { return usageError{e} })
	root.CompletionOptions.DisableDefaultCmd = true
	a.globalFlags(root)
	courses := &cobra.Command{Use: "courses", Short: "List courses or choose a course", Args: cobra.NoArgs, RunE: showGroupHelp}
	root.AddCommand(courses)
	courses.AddCommand(&cobra.Command{Use: "list", Short: "List courses without fetching their details", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		var result []lms.Course
		err := a.withClient(cmd.Context(), func(c *lms.Client) error {
			d, e := c.Dashboard()
			if e != nil {
				return e
			}
			for i := range d.Courses {
				d.Courses[i].Code = c.CourseCodes[d.Year][d.Courses[i].ID]
			}
			result = d.Courses
			return nil
		})
		if err != nil {
			return err
		}
		return a.print(result)
	}})
	scope := &cobra.Command{Use: selector, Short: "Course information, materials, reports and syllabus", Args: cobra.NoArgs, RunE: showGroupHelp}
	courses.AddCommand(scope)
	run := func(fn courseQueryFunc) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error { return a.courseQuery(cmd, args, selector, fn, a.print) }
	}
	scope.AddCommand(&cobra.Command{Use: "show", Short: "Show course information and lesson summaries", Args: cobra.NoArgs, RunE: run(func(c *lms.Client, id string, _ *cobra.Command, _ []string) (any, error) {
		// Preserve the JSON contract; only the human view is summarized.
		return c.Course(id)
	})})
	syllabus := &cobra.Command{Use: "syllabus", Short: "Read the public syllabus", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		year, _ := cmd.Flags().GetString("year")
		if year != "" && (len(year) != 4 || strings.Trim(year, "0123456789") != "") {
			return usageError{errors.New("--year must have four digits")}
		}
		urlOnly, _ := cmd.Flags().GetBool("url-only")
		return a.courseQuery(cmd, nil, selector, func(c *lms.Client, id string, _ *cobra.Command, _ []string) (any, error) {
			result, e := c.Syllabus(id, year)
			if e != nil {
				return nil, e
			}
			if !urlOnly {
				if e = lms.FetchSyllabus(cmd.Context(), result); e != nil {
					return nil, e
				}
			}
			return result, nil
		}, func(value any) error {
			if !urlOnly || a.json {
				return a.print(value)
			}
			for _, item := range appendQueryResults(nil, value) {
				fmt.Fprintln(a.out, item.(*lms.Syllabus).URL)
			}
			return nil
		})
	}}
	syllabus.Flags().String("year", "", "Academic year (defaults to the LMS-selected year)")
	syllabus.Flags().Bool("url-only", false, "Print the syllabus URL without fetching public content")
	scope.AddCommand(syllabus)
	materials := &cobra.Command{Use: "materials", Short: "Read and download course materials", ValidArgsFunction: allIDCompletion, Args: cobra.NoArgs, RunE: showGroupHelp}
	reports := &cobra.Command{Use: "reports", Short: "Read reports (lists fetch report details for deadlines)", ValidArgsFunction: allIDCompletion, Args: cobra.NoArgs, RunE: showGroupHelp}
	scope.AddCommand(materials, reports)
	materialList := &cobra.Command{Use: "list", Short: "List materials, optionally in one lesson", Args: func(cmd *cobra.Command, args []string) error {
		if err := cobra.NoArgs(cmd, args); err != nil {
			return err
		}
		_, err := lessonFlag(cmd)
		return err
	}, RunE: run(func(c *lms.Client, id string, cmd *cobra.Command, _ []string) (any, error) {
		lesson, _ := lessonFlag(cmd)
		return c.MaterialsInLesson(id, lesson)
	})}
	materialList.Flags().String("lesson", "", "Select one lesson by its exact displayed name (see course show)")
	materials.AddCommand(materialList)
	material := &cobra.Command{Use: "<material-id>", Short: "Choose an SRY material group", Args: cobra.NoArgs, RunE: showGroupHelp}
	materials.AddCommand(material)
	material.AddCommand(&cobra.Command{Use: "show", Short: "Show a material and its file IDs", Args: cobra.NoArgs, RunE: run(func(c *lms.Client, id string, cmd *cobra.Command, _ []string) (any, error) {
		return a.showMaterials(cmd, c, id)
	})})
	material.AddCommand(a.downloadCommand(selector, true))
	materials.AddCommand(a.downloadCommand(selector, false))
	reports.AddCommand(&cobra.Command{Use: "list", Short: "Read report deadlines and submission status (fetches report details)", Args: cobra.NoArgs, RunE: run(func(c *lms.Client, id string, _ *cobra.Command, _ []string) (any, error) { return c.Assignments(id) })})
	report := &cobra.Command{Use: "<report-id>", Short: "Choose a report", Args: cobra.NoArgs, RunE: showGroupHelp}
	reports.AddCommand(report)
	report.AddCommand(&cobra.Command{Use: "show", Short: "Read report instructions, submission period and status", Args: cobra.NoArgs, RunE: run(func(c *lms.Client, id string, cmd *cobra.Command, _ []string) (any, error) {
		if cmd.Parent().Name() == "*" {
			return c.AssignmentDetails(id)
		}
		return c.Assignment(cmd.Parent().Name(), id)
	})})
	var setCompletion func(*cobra.Command)
	setCompletion = func(cmd *cobra.Command) {
		if cmd.ValidArgsFunction == nil {
			cmd.ValidArgsFunction = noFileCompletion
		}
		for _, child := range cmd.Commands() {
			setCompletion(child)
		}
	}
	setCompletion(root)
	return root
}

// Delegate shell suggestions to the same static tree as execution. Only grammar
// and flags are completed; completing commands must not read sessions or LMS.
func (a *App) completeCourses(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	rest, _, err := a.scopePrefix(args)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	if len(rest) == 0 {
		items := []string{}
		if strings.HasPrefix("list", toComplete) {
			items = append(items, "list\tList courses")
		}
		if strings.HasPrefix("*", toComplete) {
			items = append(items, "*\tAll current courses (quote '*')")
		}
		return items, cobra.ShellCompDirectiveNoFileComp
	}
	selector := rest[0]
	if selector != "list" && selector != "*" && !lms.ValidCourseSelector(selector) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	if selector == "list" {
		selector = "<course>"
	}
	return a.completeScopedTree(cmd, a.courseTree(selector), append([]string{"courses"}, rest...), toComplete)
}

func lessonFlag(cmd *cobra.Command) (string, error) {
	lesson, _ := cmd.Flags().GetString("lesson")
	lesson = strings.TrimSpace(lesson)
	if cmd.Flags().Changed("lesson") && lesson == "" {
		return "", usageError{errors.New("--lesson must be an exact, non-empty lesson name")}
	}
	return lesson, nil
}
