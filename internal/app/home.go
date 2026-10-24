package app

import (
	"errors"
	"os"

	"github.com/spf13/cobra"
	"ymca/internal/lms"
)

type profileResult struct {
	lms.Account
	Verified bool `json:"authenticated_request"`
}

// The home page remains an internal data source, not a public aggregate command.
func (a *App) readHome(cmd *cobra.Command) (*lms.Dashboard, error) {
	file, _ := cmd.Flags().GetString("file")
	if file != "" {
		body, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		return lms.ParseDashboard(string(body))
	}
	var d *lms.Dashboard
	err := a.withClient(cmd.Context(), func(c *lms.Client) error { var e error; d, e = c.Dashboard(); return e })
	return d, err
}

func (a *App) homeCommands(root *cobra.Command) {
	profile := &cobra.Command{Use: "profile", Short: "Read your LMS profile", Args: cobra.NoArgs, RunE: showGroupHelp}
	show := &cobra.Command{Use: "show", Short: "Show your name, role and previous login time", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		d, err := a.readHome(cmd)
		if err != nil {
			return err
		}
		return a.print(profileResult{Account: d.Account, Verified: d.Verified})
	}}
	show.Flags().String("file", "", "Read saved home-page HTML offline (does not authenticate)")
	profile.AddCommand(show)
	topics := &cobra.Command{Use: "topics", Short: "Read LMS system announcements", Args: cobra.NoArgs, RunE: showGroupHelp}
	list := &cobra.Command{Use: "list", Short: "List dates, titles and IDs of system announcements on the LMS home page", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		d, err := a.readHome(cmd)
		if err != nil {
			return err
		}
		if !d.Verified && !a.json {
			a.line("Offline HTML (session not verified)")
		}
		return a.print(d.Topics)
	}}
	list.Flags().String("file", "", "Read saved home-page HTML offline (does not authenticate)")
	topics.AddCommand(list)
	root.AddCommand(profile, topics)
	root.AddCommand(&cobra.Command{Use: "dashboard", Hidden: true, DisableFlagParsing: true, RunE: func(_ *cobra.Command, _ []string) error {
		return usageError{errors.New("dashboard was removed; use 'ymca profile show', 'ymca topics list', 'ymca notices list' and 'ymca courses list'; use 'ymca auth status' and 'ymca keepalive status' for session status")}
	}})
}
