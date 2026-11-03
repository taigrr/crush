package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/pairing"
	"github.com/taigrr/crush/internal/qr"
)

const (
	flagInvert = "invert"
	flagPNG    = "png"
	flagScale  = "scale"
	flagTTL    = "ttl"
	flagRole   = "role"
	flagNoQR   = "no-qr"
	flagYes    = "yes"
	flagJSON   = "json"

	defaultPNGScale = 8
	pairPollMS      = 25_000
)

var pairCmd = &cobra.Command{
	Use:   "pair",
	Short: "Pair a mobile device with this cluster",
	Long: `Pair a mobile device (rush) with this Crush cluster.

Shows a one-time QR code for the phone to scan, waits for it to claim the
offer, then shows a short authentication string (emoji and digits). Approve
only if the phone shows exactly the same code. The paired device is trusted
on every node of the cluster. See docs/specs/MOBILE_PAIRING.md.`,
	Example: `
# Show a pairing QR code (light-on-dark terminal)
crush pair

# On a light terminal, valid for 5 minutes
crush pair --invert --ttl 5m

# Accept a code shown on the phone instead
crush pair accept 04106-1050R-3GG28-A1C60-T3GF6

# List and revoke devices
crush pair list
crush pair revoke dev_abc...
  `,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := checkRoleFlag(cmd); err != nil {
			return err
		}
		c, err := localServerClient(cmd)
		if err != nil {
			return err
		}
		ttl, _ := cmd.Flags().GetDuration(flagTTL)
		offer, err := c.PairCreateOffer(cmd.Context(), pairing.CreateOfferRequest{TTLSeconds: int(ttl.Seconds())})
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if err := printPairQR(cmd, offer.Payload); err != nil {
			return err
		}
		fmt.Fprintf(out, "Scan with rush, or paste:\n  %s\n", offer.Payload)
		fmt.Fprintf(out, "Expires at %s.\n\n", offer.ExpiresAt.Local().Format(time.Kitchen))
		return runPairing(cmd, c, offer)
	},
}

var pairAcceptCmd = &cobra.Command{
	Use:   "accept <code|payload>",
	Short: "Accept a pairing code shown on the phone",
	Long: `Accept the code a phone shows (a rushpair1: payload or the typed
XXXXX-XXXXX-XXXXX-XXXXX-XXXXX code), then show this node's locator QR so the
phone can connect, and confirm the short authentication string.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkRoleFlag(cmd); err != nil {
			return err
		}
		c, err := localServerClient(cmd)
		if err != nil {
			return err
		}
		offer, err := c.PairAccept(cmd.Context(), pairing.AcceptRequest{Code: args[0]})
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		fmt.Fprintln(out, "Code accepted. On the phone, scan this node's code (or enter its address):")
		if err := printPairQR(cmd, offer.InfoPayload); err != nil {
			return err
		}
		if p, err := pairing.DecodePayload(offer.InfoPayload); err == nil {
			for _, ep := range p.Endpoints {
				fmt.Fprintf(out, "  %s %s\n", ep.Kind, ep.Addr)
			}
		}
		fmt.Fprintln(out)
		return runPairing(cmd, c, offer)
	},
}

var pairListCmd = &cobra.Command{
	Use:   "list",
	Short: "List paired devices",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := localServerClient(cmd)
		if err != nil {
			return err
		}
		devices, err := c.Devices(cmd.Context())
		if err != nil {
			return err
		}
		// Every paired device has full access, whatever its stored role.
		for i := range devices {
			devices[i].Role, devices[i].Scopes = pairing.DefaultRole, devices[i].EffectiveScopes()
		}
		out := cmd.OutOrStdout()
		if asJSON, _ := cmd.Flags().GetBool(flagJSON); asJSON {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(devices)
		}
		if len(devices) == 0 {
			fmt.Fprintln(out, "No paired devices. Run `crush pair` to add one.")
			return nil
		}
		tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tDEVICE ID\tPLATFORM\tPAIRED\tSTATUS")
		for _, d := range devices {
			status := "active"
			if d.Revoked() {
				status = "revoked " + d.RevokedAt.Local().Format(time.DateOnly)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", d.Name, d.DeviceID, d.Platform, d.PairedAt.Local().Format(time.DateOnly), status)
		}
		return tw.Flush()
	},
}

var pairRevokeCmd = &cobra.Command{
	Use:   "revoke <device-id|name>",
	Short: "Revoke a paired device on every node",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := localServerClient(cmd)
		if err != nil {
			return err
		}
		devices, err := c.Devices(cmd.Context())
		if err != nil {
			return err
		}
		d, err := resolveDevice(devices, args[0])
		if err != nil {
			return err
		}
		revoked, err := c.RevokeDevice(cmd.Context(), d.DeviceID)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Revoked %s (%s) on every node.\n", revoked.Name, revoked.DeviceID)
		return nil
	},
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
	for _, c := range []*cobra.Command{pairCmd, pairAcceptCmd} {
		// Kept so existing scripts keep working; every device has full
		// access now.
		c.Flags().String(flagRole, "", "Only admin is accepted: every paired device has full access")
		_ = c.Flags().MarkDeprecated(flagRole, "device roles were removed; every paired device has full access")
		c.Flags().Bool(flagInvert, false, "Draw the QR code for dark-on-light terminals")
		c.Flags().Bool(flagNoQR, false, "Do not draw the QR code")
		c.Flags().Bool(flagYes, false, "Approve without prompting (the code is still shown)")
	}
	pairCmd.Flags().Duration(flagTTL, pairing.DefaultOfferTTL, "How long the QR code stays valid (max 10m)")
	pairListCmd.Flags().Bool(flagJSON, false, "Output as JSON")
	pairQRCmd.Flags().Bool(flagInvert, false, "Draw for dark-on-light terminals")
	pairQRCmd.Flags().String(flagPNG, "", "Write a PNG image to this path instead of printing")
	pairQRCmd.Flags().Int(flagScale, defaultPNGScale, "Pixels per module for --png")
	pairCmd.AddCommand(pairAcceptCmd, pairListCmd, pairRevokeCmd, pairQRCmd)
	rootCmd.AddCommand(pairCmd)
}

func printPairQR(cmd *cobra.Command, text string) error {
	if noQR, _ := cmd.Flags().GetBool(flagNoQR); noQR || text == "" {
		return nil
	}
	code, err := qr.Encode([]byte(text), qr.Medium)
	if err != nil {
		return err
	}
	invert, _ := cmd.Flags().GetBool(flagInvert)
	fmt.Fprintln(cmd.OutOrStdout(), code.HalfBlocks(invert))
	return nil
}

// checkRoleFlag refuses a restricted --role: roles were removed and every
// device gets full access, so silently accepting "viewer" would pair a
// device with far more access than the caller asked for.
func checkRoleFlag(cmd *cobra.Command) error {
	role, _ := cmd.Flags().GetString(flagRole)
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" || pairing.Role(role) == pairing.RoleAdmin {
		return nil
	}
	return fmt.Errorf("--role %s is no longer supported: every paired device has full access", role)
}

// runPairing follows an offer through claim, SAS confirmation, and
// approval. Interrupting it rejects the offer.
func runPairing(cmd *cobra.Command, c *client.Client, offer pairing.OfferView) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	view := offer
	defer func() {
		// Any unfinished exit (interrupt or error) cancels the offer, so it
		// cannot be completed later behind the operator's back.
		if !view.State.Terminal() {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			_, _ = c.PairReject(cleanup, offer.OfferID)
		}
	}()

	fmt.Fprintln(out, "Waiting for the phone...")
	asked := false
	for {
		next, err := c.PairWaitOffer(ctx, offer.OfferID, view.State, pairPollMS)
		if err != nil {
			return err
		}
		view = next
		switch view.State {
		case pairing.StateOpen:
			continue
		case pairing.StateClaimed:
			if asked {
				continue
			}
			asked = true
			ok, err := confirmSAS(cmd, view)
			if err != nil {
				return err
			}
			if !ok {
				if view, err = c.PairReject(ctx, offer.OfferID); err != nil {
					return err
				}
				return errors.New("pairing rejected: the codes did not match")
			}
			if view, err = c.PairApprove(ctx, offer.OfferID, ""); err != nil {
				return err
			}
			if view.State == pairing.StateClaimed {
				fmt.Fprintln(out, "Approved here; waiting for the phone to confirm...")
				continue
			}
			if view.State == pairing.StateApproved {
				printPaired(out, view)
				return nil
			}
		case pairing.StateApproved:
			printPaired(out, view)
			return nil
		case pairing.StateExpired:
			return errors.New("pairing offer expired")
		case pairing.StateAborted:
			if view.Reason == pairing.ReasonSASMismatch {
				return errors.New("the phone reported that the codes did not match (possible interception); pairing aborted")
			}
			return fmt.Errorf("pairing aborted (%s)", view.Reason)
		case pairing.StateRejected:
			return errors.New("pairing rejected")
		}
	}
}

func printPaired(out io.Writer, view pairing.OfferView) {
	name, id := "device", ""
	if view.Device != nil {
		name, id = view.Device.Name, view.Device.DeviceID
	}
	fmt.Fprintf(out, "Paired %s (%s) with full access. It is trusted on every node.\n", name, id)
}

func confirmSAS(cmd *cobra.Command, view pairing.OfferView) (bool, error) {
	out := cmd.OutOrStdout()
	if view.Device != nil {
		fmt.Fprintf(out, "\nClaimed by %q", view.Device.Name)
		if view.Device.Platform != "" {
			fmt.Fprintf(out, " (%s)", view.Device.Platform)
		}
		fmt.Fprintf(out, ", device %s\n", view.Device.DeviceID)
	}
	if view.SAS != nil {
		fmt.Fprintf(out, "\n    %s    %s\n    (%s)\n\n", strings.Join(view.SAS.Emoji, "  "), view.SAS.Digits, strings.Join(view.SAS.Names, ", "))
	}
	if yes, _ := cmd.Flags().GetBool(flagYes); yes {
		return true, nil
	}
	fmt.Fprint(out, "Does the phone show exactly the same emoji and digits? [y/N] ")
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func resolveDevice(devices []pairing.Device, ref string) (pairing.Device, error) {
	var matches []pairing.Device
	for _, d := range devices {
		if d.DeviceID == ref {
			return d, nil
		}
		if strings.HasPrefix(d.DeviceID, ref) || strings.EqualFold(d.Name, ref) {
			matches = append(matches, d)
		}
	}
	switch len(matches) {
	case 0:
		return pairing.Device{}, fmt.Errorf("no paired device matches %q", ref)
	case 1:
		return matches[0], nil
	default:
		return pairing.Device{}, fmt.Errorf("%q matches %d devices; use the full device ID", ref, len(matches))
	}
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
