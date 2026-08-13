package cmd

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"os/signal"

	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
	"github.com/pkg/browser"
	"github.com/spf13/cobra"
	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/oauth"
	"github.com/taigrr/crush/internal/oauth/copilot"
	"github.com/taigrr/crush/internal/oauth/grok"
	"github.com/taigrr/crush/internal/oauth/hyper"
)

var loginCmd = &cobra.Command{
	Aliases: []string{"auth"},
	Use:     "login [platform]",
	Short:   "Login Crush to a platform",
	Long: `Login Crush to a specified platform.
The platform should be provided as an argument.
Available platforms are: hyper, copilot, grok.`,
	Example: `
# Authenticate with Charm Hyper
crush login

# Authenticate with GitHub Copilot
crush login copilot

# Authenticate with Grok (opens a browser; reuses your grok CLI login if present)
crush login grok

# Force re-authentication even if already logged in
crush login -f copilot
  `,
	ValidArgs: []cobra.Completion{
		"hyper",
		"copilot",
		"github",
		"github-copilot",
		"grok",
	},
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, ws, cleanup, err := connectToServer(cmd)
		if err != nil {
			return err
		}
		defer cleanup()

		progressEnabled := ws.Config.Options.Progress == nil || *ws.Config.Options.Progress
		if progressEnabled && supportsProgressBar() {
			_, _ = fmt.Fprintf(os.Stderr, ansi.SetIndeterminateProgressBar)
			defer func() { _, _ = fmt.Fprintf(os.Stderr, ansi.ResetProgressBar) }()
		}

		provider := "hyper"
		if len(args) > 0 {
			provider = args[0]
		}
		force, _ := cmd.Flags().GetBool("force")
		switch provider {
		case "hyper":
			return loginHyper(c, ws.ID, force)
		case "copilot", "github", "github-copilot":
			return loginCopilot(c, ws.ID, force)
		case "grok", "xai-grok":
			return loginGrok(c, ws.ID, force)
		default:
			return fmt.Errorf("unknown platform: %s", args[0])
		}
	},
}

func init() {
	loginCmd.Flags().BoolP("force", "f", false, "Force re-authentication even if already logged in")
}

func loginHyper(c *client.Client, wsID string, force bool) error {
	ctx := getLoginContext()

	if !force {
		cfg, err := c.GetConfig(ctx, wsID)
		if err == nil && cfg != nil {
			if pc, ok := cfg.Providers.Get("hyper"); ok && pc.OAuthToken != nil {
				fmt.Println("You are already logged in to Hyper.")
				fmt.Println("Use --force to re-authenticate.")
				return nil
			}
		}
	}

	resp, err := hyper.InitiateDeviceAuth(ctx)
	if err != nil {
		return err
	}

	if clipboard.WriteAll(resp.UserCode) == nil {
		fmt.Println("The following code should be on clipboard already:")
	} else {
		fmt.Println("Copy the following code:")
	}

	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Render(resp.UserCode))
	fmt.Println()
	fmt.Println("Press enter to open this URL, and then paste it there:")
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Hyperlink(resp.VerificationURL, "id=hyper").Render(resp.VerificationURL))
	fmt.Println()
	waitEnter()
	if err := browser.OpenURL(resp.VerificationURL); err != nil {
		fmt.Println("Could not open the URL. You'll need to manually open the URL in your browser.")
	}

	fmt.Println("Exchanging authorization code...")
	refreshToken, err := hyper.PollForToken(ctx, resp.DeviceCode, resp.ExpiresIn)
	if err != nil {
		return err
	}

	fmt.Println("Exchanging refresh token for access token...")
	token, err := hyper.ExchangeToken(ctx, refreshToken)
	if err != nil {
		return err
	}

	fmt.Println("Verifying access token...")
	introspect, err := hyper.IntrospectToken(ctx, token.AccessToken)
	if err != nil {
		return fmt.Errorf("token introspection failed: %w", err)
	}
	if !introspect.Active {
		return fmt.Errorf("access token is not active")
	}

	if err := cmp.Or(
		c.SetConfigField(ctx, wsID, config.ScopeGlobal, "providers.hyper.api_key", token.AccessToken),
		c.SetConfigField(ctx, wsID, config.ScopeGlobal, "providers.hyper.oauth", token),
	); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("You're now authenticated with Hyper!")
	return nil
}

func loginCopilot(c *client.Client, wsID string, force bool) error {
	loginCtx := getLoginContext()

	if !force {
		cfg, err := c.GetConfig(loginCtx, wsID)
		if err == nil && cfg != nil {
			if pc, ok := cfg.Providers.Get("copilot"); ok && pc.OAuthToken != nil {
				fmt.Println("You are already logged in to GitHub Copilot.")
				fmt.Println("Use --force to re-authenticate.")
				return nil
			}
		}
	}

	diskToken, hasDiskToken := copilot.RefreshTokenFromDisk()
	var token *oauth.Token

	switch {
	case hasDiskToken:
		fmt.Println("Found existing GitHub Copilot token on disk. Using it to authenticate...")

		t, err := copilot.RefreshToken(loginCtx, diskToken)
		if err != nil {
			return fmt.Errorf("unable to refresh token from disk: %w", err)
		}
		token = t
	default:
		fmt.Println("Requesting device code from GitHub...")
		dc, err := copilot.RequestDeviceCode(loginCtx)
		if err != nil {
			return err
		}

		fmt.Println()
		fmt.Println("Open the following URL and follow the instructions to authenticate with GitHub Copilot:")
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().Hyperlink(dc.VerificationURI, "id=copilot").Render(dc.VerificationURI))
		fmt.Println()
		fmt.Println("Code:", lipgloss.NewStyle().Bold(true).Render(dc.UserCode))
		fmt.Println()
		fmt.Println("Waiting for authorization...")

		t, err := copilot.PollForToken(loginCtx, dc)
		if err == copilot.ErrNotAvailable {
			fmt.Println()
			fmt.Println("GitHub Copilot is unavailable for this account. To signup, go to the following page:")
			fmt.Println()
			fmt.Println(lipgloss.NewStyle().Hyperlink(copilot.SignupURL, "id=copilot-signup").Render(copilot.SignupURL))
			fmt.Println()
			fmt.Println("You may be able to request free access if eligible. For more information, see:")
			fmt.Println()
			fmt.Println(lipgloss.NewStyle().Hyperlink(copilot.FreeURL, "id=copilot-free").Render(copilot.FreeURL))
		}
		if err != nil {
			return err
		}
		token = t
	}

	if err := cmp.Or(
		c.SetConfigField(loginCtx, wsID, config.ScopeGlobal, "providers.copilot.api_key", token.AccessToken),
		c.SetConfigField(loginCtx, wsID, config.ScopeGlobal, "providers.copilot.oauth", token),
	); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("You're now authenticated with GitHub Copilot!")
	return nil
}

func loginGrok(c *client.Client, wsID string, force bool) error {
	ctx := getLoginContext()

	if !force {
		cfg, err := c.GetConfig(ctx, wsID)
		if err == nil && cfg != nil {
			if pc, ok := cfg.Providers.Get("grok"); ok && pc.OAuthToken != nil {
				fmt.Println("You are already logged in to Grok.")
				fmt.Println("Use --force to re-authenticate.")
				return nil
			}
		}
	}

	token, err := grokToken(ctx)
	if err != nil {
		return err
	}

	if err := cmp.Or(
		c.SetConfigField(ctx, wsID, config.ScopeGlobal, "providers.grok.api_key", token.AccessToken),
		c.SetConfigField(ctx, wsID, config.ScopeGlobal, "providers.grok.oauth", token),
	); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("You're now authenticated with Grok!")
	return nil
}

// grokToken obtains a Grok OAuth token. It reuses an existing grok CLI
// credential when one is present on disk (refreshing if stale);
// otherwise it runs the browser-based authorization-code + PKCE flow so
// no grok CLI install is required.
func grokToken(ctx context.Context) (*oauth.Token, error) {
	if creds, ok := grok.CredentialsFromDisk(); ok {
		fmt.Println("Found Grok CLI credentials. Verifying...")
		token := grok.TokenFromCredentials(ctx, creds)
		if token.IsExpired() {
			fmt.Println("Refreshing access token...")
			refreshed, err := grok.RefreshToken(ctx, token)
			if err != nil {
				return nil, fmt.Errorf("failed to refresh Grok token: %w", err)
			}
			token = refreshed
		}
		return token, nil
	}

	return grokBrowserLogin(ctx)
}

// grokBrowserLogin runs the loopback authorization-code + PKCE flow: it
// opens the xAI sign-in page in the browser and waits for the redirect
// to deliver the authorization code.
func grokBrowserLogin(ctx context.Context) (*oauth.Token, error) {
	session, err := grok.StartLogin(ctx)
	if err != nil {
		return nil, err
	}
	defer session.Close()

	fmt.Println("Opening your browser to sign in with xAI...")
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Hyperlink(session.AuthURL, "id=grok").Render(session.AuthURL))
	fmt.Println()
	if err := browser.OpenURL(session.AuthURL); err != nil {
		fmt.Println("Could not open the browser automatically. Open the URL above manually.")
	}
	fmt.Println("Waiting for authorization...")

	token, err := session.Wait(ctx)
	if err != nil {
		return nil, err
	}
	return token, nil
}

func getLoginContext() context.Context {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	go func() {
		<-ctx.Done()
		cancel()
		os.Exit(1)
	}()
	return ctx
}

func waitEnter() {
	_, _ = fmt.Scanln()
}
