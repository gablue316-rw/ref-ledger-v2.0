package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
)

var files = []string{
	"associations.html",
	"contact.html",
	"dashboard.html",
	"expenses.html",
	"gameStatus.html",
	"games.html",
	"importAssociations.html",
	"importGames.html",
	"importOfficials.html",
	"importSites.html",
	"index.html",
	"officials.html",
	"payments.html",
	"reports.html",
	"sites.html",
}

var (
	cssPattern = regexp.MustCompile(`navbar\.css\?v=(\d+)`)
	jsPattern  = regexp.MustCompile(`navbar\.js\?v=(\d+)`)
)

func main() {
	repositoryRoot, err := findRepositoryRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to locate repository root: %v\n", err)
		os.Exit(1)
	}

	htmlDirectory := filepath.Join(
		repositoryRoot,
		"internal",
		"html",
	)

	fmt.Printf("Repository root: %s\n", repositoryRoot)
	fmt.Printf("HTML directory:  %s\n\n", htmlDirectory)

	var modifiedFiles []string

	for _, filename := range files {
		fullPath := filepath.Join(htmlDirectory, filename)

		changed, err := updateFile(fullPath)
		if err != nil {
			fmt.Fprintf(
				os.Stderr,
				"Unable to update %s: %v\n",
				filename,
				err,
			)
			continue
		}

		if !changed {
			fmt.Printf(
				"No navbar versions found in %s\n",
				filename,
			)
			continue
		}

		relativePath, err := filepath.Rel(
			repositoryRoot,
			fullPath,
		)
		if err != nil {
			fmt.Fprintf(
				os.Stderr,
				"Unable to determine relative path for %s: %v\n",
				filename,
				err,
			)
			continue
		}

		modifiedFiles = append(
			modifiedFiles,
			relativePath,
		)

		fmt.Printf("Updated %s\n", relativePath)
	}

	if len(modifiedFiles) == 0 {
		fmt.Println("\nNo files were changed.")
		return
	}

	if err := gitAdd(repositoryRoot, modifiedFiles); err != nil {
		fmt.Fprintf(
			os.Stderr,
			"\ngit add failed: %v\n",
			err,
		)
		os.Exit(1)
	}

	fmt.Println("\nThe following files were updated and staged:")

	for _, filename := range modifiedFiles {
		fmt.Printf("  %s\n", filename)
	}
}

func updateFile(fullPath string) (bool, error) {
	fileInfo, err := os.Stat(fullPath)
	if err != nil {
		return false, err
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return false, err
	}

	originalContent := string(data)
	updatedContent := originalContent

	updatedContent = incrementVersions(
		updatedContent,
		cssPattern,
		"navbar.css?v=",
	)

	updatedContent = incrementVersions(
		updatedContent,
		jsPattern,
		"navbar.js?v=",
	)

	if updatedContent == originalContent {
		return false, nil
	}

	err = os.WriteFile(
		fullPath,
		[]byte(updatedContent),
		fileInfo.Mode(),
	)
	if err != nil {
		return false, err
	}

	return true, nil
}

func incrementVersions(
	content string,
	pattern *regexp.Regexp,
	prefix string,
) string {
	return pattern.ReplaceAllStringFunc(
		content,
		func(match string) string {
			submatches := pattern.FindStringSubmatch(match)

			if len(submatches) != 2 {
				return match
			}

			currentVersion, err := strconv.Atoi(submatches[1])
			if err != nil {
				fmt.Fprintf(
					os.Stderr,
					"Unable to parse version in %q: %v\n",
					match,
					err,
				)

				return match
			}

			newVersion := currentVersion + 2

			fmt.Printf(
				"  %s%d -> %s%d\n",
				prefix,
				currentVersion,
				prefix,
				newVersion,
			)

			return fmt.Sprintf(
				"%s%d",
				prefix,
				newVersion,
			)
		},
	)
}

func gitAdd(
	repositoryRoot string,
	files []string,
) error {
	args := append([]string{"add", "--"}, files...)

	command := exec.Command("git", args...)

	// Run git add from the repository root.
	command.Dir = repositoryRoot

	command.Stdout = os.Stdout
	command.Stderr = os.Stderr

	return command.Run()
}

func findRepositoryRoot() (string, error) {
	currentDirectory, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		goModPath := filepath.Join(
			currentDirectory,
			"go.mod",
		)

		gitPath := filepath.Join(
			currentDirectory,
			".git",
		)

		if pathExists(goModPath) || pathExists(gitPath) {
			return currentDirectory, nil
		}

		parentDirectory := filepath.Dir(currentDirectory)

		if parentDirectory == currentDirectory {
			break
		}

		currentDirectory = parentDirectory
	}

	return "", fmt.Errorf(
		"could not find go.mod or .git in the current directory or any parent directory",
	)
}

func pathExists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}
