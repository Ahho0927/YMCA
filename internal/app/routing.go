package app

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"ymca/internal/lms"
)

var resourceIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// Parse a union of the tree's flags only to locate positional scopes. The final
// Cobra leaf still validates its own flags/arguments before any session access.
// Deriving flags from the tree avoids a second hard-coded list of flag arities.
func treePositionals(tree *cobra.Command, args []string) ([]string, error) {
	flags := pflag.NewFlagSet("scope", pflag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolP("help", "h", false, "")
	var visit func(*cobra.Command)
	visit = func(cmd *cobra.Command) {
		add := func(f *pflag.Flag) {
			if flags.Lookup(f.Name) == nil {
				copy := *f
				flags.AddFlag(&copy)
			}
		}
		cmd.PersistentFlags().VisitAll(add)
		cmd.Flags().VisitAll(add)
		for _, child := range cmd.Commands() {
			visit(child)
		}
	}
	visit(tree)
	if err := flags.Parse(args); err != nil {
		return nil, usageError{err}
	}
	return flags.Args(), nil
}

// Rename one placeholder command to the actual selected ID. Its leaf handlers
// read their parent's name, so execution/help/completion share the same tree.
func bindResourceScope(tree *cobra.Command, args []string) error {
	pos, err := treePositionals(tree, args)
	if err != nil {
		return err
	}
	index := 0
	placeholder := ""
	switch {
	case len(pos) >= 2 && pos[0] == "notices":
		index, placeholder = 1, "<notice-id>"
	case len(pos) >= 4 && pos[0] == "courses" && pos[2] == "materials":
		index, placeholder = 3, "<material-id>"
	case len(pos) >= 4 && pos[0] == "courses" && pos[2] == "reports":
		index, placeholder = 3, "<report-id>"
	default:
		return nil
	}
	id := pos[index]
	switch id {
	case "list":
		return nil
	case "download":
		if placeholder == "<material-id>" {
			return nil
		}
		return usageError{fmt.Errorf("unsupported action; use '%s <id> show'", strings.Join(pos[:index], " "))}
	case "show":
		return usageError{fmt.Errorf("the ID comes before the action; use 'ymca %s <id> show'", strings.Join(pos[:index], " "))}
	}
	if id != "*" && !resourceIdentifier.MatchString(id) {
		return usageError{fmt.Errorf("invalid resource ID (use a single ID or quoted literal '*', not a partial glob); use 'ymca %s <id> show'", strings.Join(pos[:index], " "))}
	}
	group, _, err := tree.Find(pos[:index])
	if err != nil {
		return usageError{err}
	}
	for _, child := range group.Commands() {
		if child.Name() == placeholder {
			group.RemoveCommand(child)
			child.Use = id
			if id == "*" {
				child.Short = "Choose all matching items"
			}
			if id == "*" && placeholder == "<material-id>" {
				for _, action := range child.Commands() {
					if action.Name() == "download" {
						action.Short = "Download files from all selected materials; quote '*'"
						action.Flags().String("lesson", "", "Limit all materials to one exact lesson name")
					}
				}
			}
			group.AddCommand(child)
			return nil
		}
	}
	return usageError{fmt.Errorf("unsupported resource scope")}
}

func (a *App) completeScopedTree(cmd *cobra.Command, tree *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// Only completed preceding tokens bind IDs; a partial current token must
	// not become a literal identifier suggestion.
	if err := bindResourceScope(tree, args); err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	// Placeholder command names describe help syntax, not literal ID suggestions.
	var hide func(*cobra.Command)
	hide = func(c *cobra.Command) {
		if strings.HasPrefix(c.Name(), "<") {
			c.Hidden = true
		}
		for _, child := range c.Commands() {
			hide(child)
		}
	}
	hide(tree)
	var out, stderr bytes.Buffer
	tree.SetOut(&out)
	tree.SetErr(&stderr)
	tree.SetArgs(append(append([]string{"__complete"}, args...), toComplete))
	if err := tree.ExecuteContext(cmd.Context()); err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	directive, err := strconv.Atoi(strings.TrimPrefix(lines[len(lines)-1], ":"))
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	suggestions := []string{}
	for _, line := range lines[:len(lines)-1] {
		name := strings.SplitN(line, "\t", 2)[0]
		if strings.HasPrefix(toComplete, "-") && (name == "--json" || name == "--state-dir" || name == "--capture-dir") {
			// The outer opaque command already contributes these global flags.
			continue
		}
		suggestions = append(suggestions, line)
	}
	return suggestions, cobra.ShellCompDirective(directive)
}

func (a *App) noticeTree() *cobra.Command {
	root := &cobra.Command{Use: "ymca", SilenceErrors: true, SilenceUsage: true}
	root.SetOut(a.out)
	root.SetErr(a.err)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	root.CompletionOptions.DisableDefaultCmd = true
	a.globalFlags(root)
	notices := &cobra.Command{Use: "notices", Short: "Read the global LMS notice box", ValidArgsFunction: allIDCompletion, Args: cobra.NoArgs, RunE: showGroupHelp}
	root.AddCommand(notices)
	notices.AddCommand(&cobra.Command{Use: "list", Short: "List notices", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		var result []lms.Notice
		err := a.withClient(cmd.Context(), func(c *lms.Client) error { var e error; result, e = c.Notices(""); return e })
		if err != nil {
			return err
		}
		return a.print(result)
	}})
	notice := &cobra.Command{Use: "<notice-id>", Short: "Choose a notice", Args: cobra.NoArgs, RunE: showGroupHelp}
	notices.AddCommand(notice)
	notice.AddCommand(&cobra.Command{Use: "show", Short: "Read a notice and its attachment metadata", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		var result any
		batch := cmd.Parent().Name() == "*"
		err := a.withClient(cmd.Context(), func(c *lms.Client) error {
			if !batch {
				var e error
				result, e = c.Notice(cmd.Parent().Name(), "")
				return e
			}
			list, e := c.Notices("")
			if e != nil {
				return e
			}
			ids := []string{}
			byID := map[string]lms.Notice{}
			for _, n := range list {
				ids = append(ids, n.ID)
				byID[n.ID] = n
			}
			result, e = collectQueries(cmd.Context(), ids, false, func(id string) (any, error) { return c.ReadNotice(byID[id]) })
			return e
		})
		if result != nil && (err == nil || batch) {
			if e := a.print(result); e != nil {
				return e
			}
		}
		return err
	}})
	var visit func(*cobra.Command)
	visit = func(cmd *cobra.Command) {
		if cmd.ValidArgsFunction == nil {
			cmd.ValidArgsFunction = noFileCompletion
		}
		for _, child := range cmd.Commands() {
			visit(child)
		}
	}
	visit(root)
	return root
}

func (a *App) routeNotices(cmd *cobra.Command, args []string) error {
	rest, help, err := a.scopePrefix(args)
	if err != nil {
		return err
	}
	tree := a.noticeTree()
	argv := append([]string{"notices"}, rest...)
	if help {
		argv = append(argv, "--help")
	}
	if err = bindResourceScope(tree, argv); err != nil {
		return err
	}
	tree.SetArgs(argv)
	return tree.ExecuteContext(cmd.Context())
}

func (a *App) completeNotices(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	rest, _, err := a.scopePrefix(args)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return a.completeScopedTree(cmd, a.noticeTree(), append([]string{"notices"}, rest...), toComplete)
}

func allIDCompletion(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 && strings.HasPrefix("*", toComplete) {
		return []string{"*\tAll matching items (quote '*')"}, cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}
