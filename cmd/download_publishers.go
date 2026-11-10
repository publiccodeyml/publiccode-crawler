package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/italia/publiccode-crawler/v4/common"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func init() {
	rootCmd.AddCommand(downloadPublishersCmd)
}

type repolistType struct {
	Registrati []struct {
		IPA string `yaml:"ipa"`
		URL string `yaml:"url"`
		PEC string `yaml:"pec"`
	} `yaml:"registrati"`
}

var downloadPublishersCmd = &cobra.Command{
	Use:           "download-publishers REPOLIST_URL DEST_FILE",
	Short:         "Download the list of repos and orgs from the onboarding portal.",
	Long:          `Download the list of repos and orgs from the onboarding portal and convert it into a publishers.yml.`,
	Args:          cobra.ExactArgs(2),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(_ *cobra.Command, args []string) error {
		return downloadPublishers(args[0], args[1])
	},
}

func downloadPublishers(repolistURL, destination string) error {
	var publishers []common.Publisher

	if _, err := os.Stat(destination); err == nil {
		data, err := os.ReadFile(destination)
		if err != nil {
			return fmt.Errorf("read %s: %w", destination, err)
		}
		//nolint:musttag // false positive
		_ = yaml.Unmarshal(data, &publishers)
	}

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, repolistURL, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("download publisher list: %s", resp.Status)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var repolist repolistType

	err = yaml.Unmarshal(bodyBytes, &repolist)
	if err != nil {
		return err
	}

REPOLIST:
	for _, entry := range repolist.Registrati {
		for idx, publisher := range publishers {
			if publisher.ID == entry.IPA {
				parsedURL, _ := url.Parse(entry.URL)
				// If this Id is already known, append this URL to the existing item
				publishers[idx].Sources = append(publisher.Sources, common.CodeHosting{
					URL:    *parsedURL,
					Driver: common.InferVCSDriver(*parsedURL),
					Group:  true,
				})

				continue REPOLIST
			}
		}

		parsedURL, _ := url.Parse(entry.URL)
		// If this IPA code is not known, append a new publisher item
		publishers = append(publishers, common.Publisher{
			Name: entry.IPA,
			ID:   entry.IPA,
			Sources: []common.CodeHosting{{
				URL:    *parsedURL,
				Driver: common.InferVCSDriver(*parsedURL),
				Group:  true,
			}},
		})
	}

	// Write to the destination file
	outFile, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer outFile.Close()

	data, err := yaml.Marshal(publishers)
	if err != nil {
		return err
	}

	if _, err = outFile.Write(data); err != nil {
		return err
	}

	return nil
}
