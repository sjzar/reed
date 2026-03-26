package reed

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sjzar/reed/pkg/version"
)

func init() {
	versionCmd.Flags().BoolVarP(&versionM, "module", "m", false, "include Go build settings and dependency module versions")
}

var versionM bool
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show Reed version and build information.",
	Run: func(cmd *cobra.Command, args []string) {
		if versionM {
			fmt.Print(version.GetMore(true))
		} else {
			fmt.Printf("reed %s", version.GetMore(false))
		}
	},
}
