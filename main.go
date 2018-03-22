package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/jinzhao1994/glog"
	"gopkg.in/ini.v1"
	"gopkg.in/yaml.v2"
)

type RepoConfig struct {
	Directory string
	Remote    string
}

type Config struct {
	Repos []RepoConfig
}

type Flag struct {
	RootDir string
	Update  bool
	Upgrade bool
}

var config Config
var flags Flag

func init() {
	flag.StringVar(&flags.RootDir, "dir", "/Volumes/Code/src", "root dir to check")
	flag.BoolVar(&flags.Update, "update", true, "update config file")
	flag.BoolVar(&flags.Upgrade, "upgrade", true, "upgrade repositories")
}

func gitClone(repo RepoConfig) (string, error) {
	_, err := os.Stat(filepath.Join(repo.Directory, ".git"))
	if err == nil {
		return "", filepath.SkipDir
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	cmd := exec.Command("git", "clone", repo.Remote, repo.Directory)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	stderrBytes, err := ioutil.ReadAll(stderr)
	if err != nil {
		return "", err
	}
	if err := cmd.Wait(); err != nil {
		return string(stderrBytes), err
	}
	return string(stderrBytes), nil
}

func gitFetch(repo RepoConfig) (string, error) {
	cmd := exec.Command("git", "fetch")
	cmd.Dir = repo.Directory
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	stderrBytes, err := ioutil.ReadAll(stderr)
	if err != nil {
		return "", err
	}
	if err := cmd.Wait(); err != nil {
		return string(stderrBytes), err
	}
	return string(stderrBytes), nil
}

const gitStatusTemplateStr = `^On branch master
(Your branch is behind 'origin/master' by \d+ commit(s?), and can be fast-forwarded\.|Your branch is up-to-date with 'origin/master'\.)
(  \(use "git pull" to update your local branch\)\n)?
nothing to commit, working tree clean
$`

var gitStatusTemplateRe = regexp.MustCompile(gitStatusTemplateStr)

func gitPull(repo RepoConfig) (string, error) {
	// Check if can pull
	cmd := exec.Command("git", "status")
	cmd.Dir = repo.Directory
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	stdoutBytes, err := ioutil.ReadAll(stdout)
	if err != nil {
		return "", err
	}
	if err := cmd.Wait(); err != nil {
		return "", err
	}
	// Skip this dir
	if !gitStatusTemplateRe.Match(stdoutBytes) {
		return "", filepath.SkipDir
	}
	// Run git pull
	cmd = exec.Command("git", "pull")
	cmd.Dir = repo.Directory
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	stderrBytes, err := ioutil.ReadAll(stderr)
	if err != nil {
		return "", err
	}
	if err := cmd.Wait(); err != nil {
		return string(stderrBytes), err
	}
	return string(stderrBytes), nil
}

func upgrade() error {
	glog.Info("Upgrading")
	// Calculate time
	defer func(st time.Time) {
		ed := time.Now()
		d := ed.Sub(st)
		glog.Infof("Upgraded. Takes %.2f seconds.", d.Seconds())
	}(time.Now())
	// Check each repository
	for _, repo := range config.Repos {
		if stderr, err := gitClone(repo); err == filepath.SkipDir {
			// No log for exist repos
		} else if err != nil {
			glog.Errorf("Clone to %s failed: %v\n%s", repo.Directory, err, stderr)
			continue
		} else {
			glog.Infof("Clone to %s finished", repo.Directory)
		}
		if stderr, err := gitFetch(repo); err != nil {
			glog.Errorf("Fetch in %s failed: %v\n%s", repo.Directory, err, stderr)
			continue
		} else {
			glog.Infof("Fetch in %s finished", repo.Directory)
		}
		if stderr, err := gitPull(repo); err == filepath.SkipDir {
			glog.Warningf("Upgrade in %s skipped", repo.Directory)
		} else if err != nil {
			glog.Errorf("Upgrade in %s failed: %v\n%s", repo.Directory, err, stderr)
			continue
		} else {
			glog.Infof("Upgrade in %s finished", repo.Directory)
		}
	}
	return nil
}

func remoteDir(path string) (string, error) {
	gitConfigFile, err := os.Open(filepath.Join(path, "config"))
	if err != nil {
		return "", err
	}
	defer gitConfigFile.Close()
	gitConfig, err := ini.Load(gitConfigFile)
	if err != nil {
		return "", err
	}
	section := gitConfig.Section("remote \"origin\"")
	if section == nil {
		return "", errors.New(fmt.Sprintf("can't find \"origin\" in %s", path))
	}
	key := section.Key("url")
	if key == nil {
		return "", errors.New(fmt.Sprintf("can't find \"origin.url\" in %s", path))
	}
	return key.Value(), nil
}

func update() error {
	glog.Infof("Updating...")
	// Calculate time
	defer func(st time.Time) {
		ed := time.Now()
		d := ed.Sub(st)
		glog.Infof("Updated %d repositories. Takes %.2f seconds.", len(config.Repos), d.Seconds())
	}(time.Now())
	// Recursively check all path
	err := filepath.Walk(flags.RootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			glog.Error("Error in file path walk: ", err)
			return err
		}
		if !info.IsDir() || info.Name() != ".git" {
			return nil
		}
		remote, err := remoteDir(path)
		if err != nil {
			return err
		}
		config.Repos = append(config.Repos, RepoConfig{
			Directory: filepath.Dir(path),
			Remote:    remote,
		})
		return filepath.SkipDir
	})
	if err != nil {
		return err
	}
	// Merge repositories
	repoDict := map[string]string{}
	for _, repo := range config.Repos {
		repoDict[repo.Directory] = repo.Remote
	}
	config.Repos = make([]RepoConfig, 0, len(repoDict))
	for dir, remote := range repoDict {
		config.Repos = append(config.Repos, RepoConfig{dir, remote})
	}
	sort.Slice(config.Repos, func(i, j int) bool {
		return config.Repos[i].Directory < config.Repos[j].Directory
	})
	return nil
}

func do() error {
	// Read config file
	filename := filepath.Join(flags.RootDir, "repositories.txt")
	configYAML, err := ioutil.ReadFile(filename)
	if err != nil {
		return err
	}
	err = yaml.Unmarshal(configYAML, &config)
	if err != nil {
		return err
	}
	// Update repositories list
	if flags.Update {
		if err := update(); err != nil {
			return err
		}
	}
	// Upgrade repositories
	if flags.Upgrade {
		if err := upgrade(); err != nil {
			return err
		}
	}
	// Update config file
	if flags.Update {
		configYAML, err := yaml.Marshal(config)
		if err != nil {
			return err
		}
		err = ioutil.WriteFile(filename, configYAML, 0644)
		if err != nil {
			return err
		}
	}
	return nil
}

func main() {
	flag.Parse()
	glog.Info("Recursively check code in directory ", flags.RootDir)
	if err := do(); err != nil {
		glog.Error(err)
	}
}
