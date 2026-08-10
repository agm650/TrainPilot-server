package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newExportRollingStockCommand(a *commandContext) *cobra.Command {
	return exportCommand("export-rolling-stock <output-file>", "Export rolling stock", a.clientExportRollingStock)
}

func newExportLayoutCommand(a *commandContext) *cobra.Command {
	return exportCommand("export-layout <output-file>", "Export layout", a.clientExportLayout)
}

func exportCommand(use, short string, download func(*cobra.Command) ([]byte, error)) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: short, Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := download(cmd)
			if err != nil {
				return err
			}
			if err := os.WriteFile(args[0], data, 0o600); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (%d bytes)\n", args[0], len(data))
			return nil
		},
	}
}

func (a *commandContext) clientExportRollingStock(cmd *cobra.Command) ([]byte, error) {
	return a.client.ExportRollingStock(cmd.Context())
}

func (a *commandContext) clientExportLayout(cmd *cobra.Command) ([]byte, error) {
	return a.client.ExportLayout(cmd.Context())
}

func newImportRollingStockCommand(a *commandContext) *cobra.Command {
	return importCommand("import-rolling-stock <input-file>", "Import rolling stock", func(cmd *cobra.Command, data []byte, replace bool) error {
		return a.client.ImportRollingStock(cmd.Context(), data, replace)
	})
}

func newImportLayoutCommand(a *commandContext) *cobra.Command {
	return importCommand("import-layout <input-file>", "Import layout", func(cmd *cobra.Command, data []byte, replace bool) error {
		return a.client.ImportLayout(cmd.Context(), data, replace)
	})
}

func importCommand(use, short string, upload func(*cobra.Command, []byte, bool) error) *cobra.Command {
	var replace bool
	cmd := &cobra.Command{
		Use: use, Short: short, Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			return upload(cmd, data, replace)
		},
	}
	cmd.Flags().BoolVar(&replace, "replace", false, "replace the existing library instead of merging")
	return cmd
}
