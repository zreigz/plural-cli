package wkspace

import (
	"regexp"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pluralsh/plural/pkg/executor"
	"github.com/pluralsh/plural/pkg/utils"
	"github.com/pluralsh/plural/pkg/utils/git"
	"github.com/pluralsh/plural/pkg/utils/pathing"
)

type checker func(s string) bool

var alwaysErr checker = func(s string) bool { return false }


func (c checker) execSuppressed(command string, args ...string) (err error) {
	for retry := 2; retry >= 0; retry-- {
		utils.Highlight("%s %s ~> ", command, strings.Join(args, " "))
		cmd, out := executor.SuppressedCommand(command, args...)
		err = executor.RunCommand(cmd, out)
		if err == nil || c(out.Format()) {
			break
		}
		fmt.Printf("retrying command, number of retries remaining: %d\n", retry)
	}

	return
}

func (w *Workspace) DestroyHelm() error {
	// ensure current kubeconfig is correct before destroying stuff
	w.Provider.KubeConfig()
	name := w.Installation.Repository.Name

	ns := w.Config.Namespace(name)
	if err := alwaysErr.execSuppressed("helm", "get", "values", name, "-n", ns); err != nil {
		fmt.Println("Helm already uninstalled, continuing...")
		return nil
	}

	r, _ := regexp.Compile("release.*not found")
	var ignoreNotFound checker = func(s string) bool { return r.MatchString(s) }
	return ignoreNotFound.execSuppressed("helm", "del", name, "-n", ns)
}

func (w *Workspace) Bounce() error {
	return w.ToMinimal().BounceHelm()
}

func (w *Workspace) HelmDiff() error {
	return w.ToMinimal().DiffHelm()
}

func (w *Workspace) Destroy() error {
	if err := w.DestroyHelm(); err != nil {
		return err
	}

	if err := w.DestroyTerraform(); err != nil {
		return err
	}

	return w.Reset()
}

func (w *Workspace) Reset() error {
	repo := w.Installation.Repository
	repoRoot, err := git.Root()
	if err != nil {
		return err
	}

	deployfile := pathing.SanitizeFilepath(filepath.Join(repoRoot, repo.Name, "deploy.hcl"))
	os.Remove(deployfile)
	return nil
}

func (w *Workspace) DestroyTerraform() error {
	repo := w.Installation.Repository
	path, err := filepath.Abs(pathing.SanitizeFilepath(filepath.Join(repo.Name, "terraform")))
	if err != nil {
		return err
	}

	time.AfterFunc(1*time.Minute, func() {
		kube, err := utils.Kubernetes()
		if err != nil {
			fmt.Println("could not set up k8s client due to %s", err)
			return
		}

		ns := w.Config.Namespace(repo.Name)
		kube.FinalizeNamespace(ns)
	})

	os.Chdir(path)
	if err := alwaysErr.execSuppressed("terraform", "init", "-upgrade"); err != nil {
		return err
	}

	return alwaysErr.execSuppressed("terraform", "destroy", "-auto-approve")
}
