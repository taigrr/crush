package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/taigrr/crush/internal/qr"
)

const (
	flagInvert = "invert"
	flagPNG    = "png"
	flagScale  = "scale"

	defaultPNGScale = 8
)

var pairCmd = &cobra.Command{
	Use:   "pair",
	Short: "Pair mobile devices with this cluster",
	Long:  "Pair mobile devices (rush) with this Crush cluster by QR code. See docs/specs/MOBILE_PAIRING.md.",
}

var pairQRCmd = &cobra.Command{
	Use:   "qr [text|-]",
	Short: "Render text as a QR code in the terminal",
	Long: `Render text as a QR code using half-block characters.

By default the code is drawn for light-on-dark terminals; use --invert on
dark-on-light terminals. With --png the code is written as an image instead.`,
	Example: `
# Render a pairing payload
crush pair qr 'rushpair1:...'

# Read from stdin, for a light terminal
echo hello | crush pair qr --invert -

# Write a PNG
crush pair qr --png code.png 'rushpair1:...'
  `,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		text, err := pairQRInput(cmd.InOrStdin(), args)
		if err != nil {
			return err
		}
		code, err := qr.Encode([]byte(text), qr.Medium)
		if err != nil {
			return err
		}
		if path, _ := cmd.Flags().GetString(flagPNG); path != "" {
			scale, _ := cmd.Flags().GetInt(flagScale)
			return writeQRPNG(code, path, scale)
		}
		invert, _ := cmd.Flags().GetBool(flagInvert)
		fmt.Fprintln(cmd.OutOrStdout(), code.HalfBlocks(invert))
		return nil
	},
}

func init() {
	pairQRCmd.Flags().Bool(flagInvert, false, "Draw for dark-on-light terminals")
	pairQRCmd.Flags().String(flagPNG, "", "Write a PNG image to this path instead of printing")
	pairQRCmd.Flags().Int(flagScale, defaultPNGScale, "Pixels per module for --png")
	pairCmd.AddCommand(pairQRCmd)
	rootCmd.AddCommand(pairCmd)
}

func pairQRInput(stdin io.Reader, args []string) (string, error) {
	if len(args) == 1 && args[0] != "-" {
		return args[0], nil
	}
	data, err := io.ReadAll(io.LimitReader(stdin, int64(qr.Capacity(qr.MaxVersion, qr.Low))+1))
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	text := strings.TrimRight(string(data), "\r\n")
	if text == "" {
		return "", errors.New("nothing to encode")
	}
	return text, nil
}

func writeQRPNG(code *qr.Code, path string, scale int) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if err := code.WritePNG(f, scale); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
