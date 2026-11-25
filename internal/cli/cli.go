package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/chickenzord/finorama/internal/store"
	"github.com/chickenzord/finorama/internal/syncer"
	"github.com/chickenzord/finorama/internal/vault"
	"github.com/chickenzord/goksei"
	"golang.org/x/term"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

type CLI struct {
	store   *store.Store
	vault   *vault.Vault
	syncer  *syncer.Syncer
	dataDir string
	reader  *bufio.Reader
	printer *message.Printer
}

func New(st *store.Store, v *vault.Vault, sync *syncer.Syncer, dataDir string) *CLI {
	return &CLI{
		store:   st,
		vault:   v,
		syncer:  sync,
		dataDir: dataDir,
		reader:  bufio.NewReader(os.Stdin),
		printer: message.NewPrinter(language.Indonesian),
	}
}

// UnlockVault prompts user for master passphrase to initialize or unlock.
func (c *CLI) UnlockVault() ([]byte, error) {
	init, err := c.store.IsInitialized()
	if err != nil {
		return nil, fmt.Errorf("check db init: %w", err)
	}

	if !init {
		fmt.Println("Finorama vault is not initialized yet.")
		for {
			p1, err := c.promptPassword("Choose Master Passphrase: ")
			if err != nil {
				return nil, err
			}
			if len(p1) < 6 {
				fmt.Println("Passphrase must be at least 6 characters.")
				continue
			}
			p2, err := c.promptPassword("Confirm Master Passphrase: ")
			if err != nil {
				return nil, err
			}
			if p1 != p2 {
				fmt.Println("Passphrases do not match. Try again.")
				continue
			}

			key, err := c.store.InitializeMaster(p1)
			if err != nil {
				return nil, fmt.Errorf("init master: %w", err)
			}
			c.vault.Unlock(key)
			fmt.Println("Vault successfully initialized.")
			return key, nil
		}
	}

	for {
		p, err := c.promptPassword("Enter Master Passphrase: ")
		if err != nil {
			return nil, err
		}
		key, valid, err := c.store.VerifyAndDeriveKey(p)
		if err != nil {
			return nil, fmt.Errorf("verify passphrase: %w", err)
		}
		if !valid {
			fmt.Println("Incorrect passphrase. Try again.")
			continue
		}
		c.vault.Unlock(key)
		return key, nil
	}
}

// AddAccount prompts for credentials, tests connection with goksei, and stores encrypted.
func (c *CLI) AddAccount() error {
	key, err := c.UnlockVault()
	if err != nil {
		return err
	}

	fmt.Println("\n--- Connect New KSEI Account ---")
	name, err := c.promptLine("Account Label (e.g. Primary): ")
	if err != nil {
		return err
	}
	username, err := c.promptLine("AKSES KSEI Username/Email: ")
	if err != nil {
		return err
	}
	password, err := c.promptPassword("AKSES KSEI Password: ")
	if err != nil {
		return err
	}

	fmt.Print("\nTesting KSEI credentials against AKSES-KSEI API...")
	testClient := goksei.NewClient(goksei.ClientOpts{
		Username:      username,
		Password:      password,
		PlainPassword: true,
		Timeout:       20 * time.Second,
	})

	_, testErr := testClient.GetPortfolioSummary()
	if testErr != nil {
		fmt.Printf(" FAILED\n[!] KSEI login/verification error: %v\n", testErr)
		confirm, _ := c.promptLine("Do you still want to save this account? (y/N): ")
		if strings.ToLower(confirm) != "y" {
			fmt.Println("Aborted. Account not saved.")
			return nil
		}
	} else {
		fmt.Println(" SUCCESS! Credentials verified.")
	}

	acc, err := c.store.AddAccount(key, name, username, password)
	if err != nil {
		return fmt.Errorf("failed to save account: %w", err)
	}

	fmt.Printf("Account '%s' (ID: %d) saved.\n", acc.Name, acc.ID)

	// Offer to sync immediately
	syncNow, _ := c.promptLine("Capture and sync initial balances now? (Y/n): ")
	if syncNow == "" || strings.ToLower(syncNow) == "y" {
		fmt.Print("Syncing holdings...")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := c.syncer.SyncAccountByID(ctx, acc.ID); err != nil {
			fmt.Printf(" error: %v\n", err)
		} else {
			fmt.Println(" done!")
			_ = c.showHoldings(acc.ID)
		}
	}

	return nil
}

// ShowHoldings interactively selects an account and displays latest holdings.
func (c *CLI) ShowHoldings(accIDArg int64) error {
	accounts, err := c.store.ListAccounts()
	if err != nil {
		return fmt.Errorf("list accounts: %w", err)
	}
	if len(accounts) == 0 {
		fmt.Println("No accounts configured yet. Run 'finorama account add' first.")
		return nil
	}

	targetID := accIDArg
	if targetID <= 0 {
		fmt.Println("\nAvailable Accounts:")
		for i, a := range accounts {
			lastSync := "Never"
			if a.LastSyncedAt != nil {
				lastSync = a.LastSyncedAt.Format("2006-01-02 15:04")
			}
			fmt.Printf("  [%d] ID: %d | %s (%s) | Last Synced: %s\n", i+1, a.ID, a.Name, a.Username, lastSync)
		}

		for {
			choiceStr, err := c.promptLine("Select account number or ID: ")
			if err != nil {
				return err
			}
			idx, err := strconv.Atoi(choiceStr)
			if err == nil {
				if idx >= 1 && idx <= len(accounts) {
					targetID = accounts[idx-1].ID
					break
				}
				for _, a := range accounts {
					if a.ID == int64(idx) {
						targetID = a.ID
						break
					}
				}
				if targetID > 0 {
					break
				}
			}
			fmt.Println("Invalid selection. Try again.")
		}
	}

	return c.showHoldings(targetID)
}

func (c *CLI) showHoldings(accountID int64) error {
	acc, err := c.store.GetAccountByID(accountID)
	if err != nil {
		return fmt.Errorf("account not found: %w", err)
	}

	holdings, err := c.store.GetHoldingsByAccountID(accountID)
	if err != nil {
		return fmt.Errorf("get holdings: %w", err)
	}

	fmt.Printf("\n============================================================\n")
	fmt.Printf("Holdings for: %s (%s)\n", acc.Name, acc.Username)
	if acc.LastSyncedAt != nil {
		fmt.Printf("Last Synced:  %s\n", acc.LastSyncedAt.Format("2006-01-02 15:04:05"))
	}
	fmt.Printf("============================================================\n")

	if len(holdings) == 0 {
		fmt.Println("No holdings stored. Use 'finorama sync' to capture balances.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "TYPE\tCODE\tNAME\tPARTICIPANT\tQTY\tPRICE (IDR)\tTOTAL (IDR)")
	fmt.Fprintln(w, "----\t----\t----\t-----------\t---\t-----------\t-----------")

	var grandTotal float64
	for _, h := range holdings {
		grandTotal += h.TotalValue
		name := h.SecurityName
		if len(name) > 28 {
			name = name[:25] + "..."
		}
		part := h.Participant
		if len(part) > 20 {
			part = part[:17] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			h.AssetType,
			h.SecurityCode,
			name,
			part,
			c.printer.Sprintf("%.2f", h.Quantity),
			c.printer.Sprintf("%.0f", h.Price),
			c.printer.Sprintf("%.0f", h.TotalValue),
		)
	}
	w.Flush()

	fmt.Printf("------------------------------------------------------------\n")
	fmt.Printf("TOTAL PORTFOLIO VALUE: IDR %s\n", c.printer.Sprintf("%d", int64(grandTotal)))
	fmt.Printf("============================================================\n")
	return nil
}

// SyncManual syncs accounts directly from CLI.
func (c *CLI) SyncManual(accountID int64) error {
	_, err := c.UnlockVault()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if accountID > 0 {
		fmt.Printf("Syncing account ID %d...\n", accountID)
		if err := c.syncer.SyncAccountByID(ctx, accountID); err != nil {
			return err
		}
		return c.showHoldings(accountID)
	}

	fmt.Println("Syncing all active accounts...")
	if err := c.syncer.SyncAll(ctx); err != nil {
		return err
	}
	fmt.Println("Sync completed successfully.")
	return nil
}

func (c *CLI) promptLine(prompt string) (string, error) {
	fmt.Print(prompt)
	line, err := c.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (c *CLI) promptPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	bytePassword, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(bytePassword)), nil
}
