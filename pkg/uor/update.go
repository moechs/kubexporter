package uor

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/bakito/kubexporter/pkg/client"
	"github.com/bakito/kubexporter/pkg/render"
	"github.com/bakito/kubexporter/pkg/types"
	"github.com/olekukonko/tablewriter"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func Update(config *types.Config) error {
	err := config.Validate()
	if err != nil {
		return err
	}

	var files []string
	err = filepath.Walk(config.Target, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && filepath.Ext(path) == "."+config.OutputFormat() {
			files = append(files, path)
		}

		return nil
	})
	if err != nil {
		return err
	}

	ac, err := client.NewApiClient(config)
	if err != nil {
		return err
	}

	table := render.Table()
	table.SetHeader([]string{"File", "Owner Kind", "Owner Name", "UID From", "UID To"})

	ctx := context.TODO()
	for _, file := range files {
		err2 := updateFile(ctx, config, file, ac, table)
		if err2 != nil {
			return err2
		}
	}

	if table.NumLines() == 0 {
		println("No changed owner references found")
	} else {
		table.Render()
	}
	return nil
}

func updateFile(ctx context.Context, config *types.Config, file string, ac *client.ApiClient, table *tablewriter.Table) error {
	fileName := strings.Replace(file, config.Target+"/", "", 1)
	us, err := read(file)
	if err != nil {
		return err
	}
	refs := us.GetOwnerReferences()
	owners := make(map[string]*unstructured.Unstructured)
	changed := false
	if len(refs) > 0 {
		for i := range refs {
			ref := &refs[i]
			owner, err := findOwner(ctx, ac, owners, ref, us)
			if err != nil {
				errMsg := "<ERROR>"
				if errors.IsNotFound(err) {
					errMsg = "<NOT FOUND>"
				}
				table.Append([]string{
					fileName,
					ref.Kind,
					ref.Name,
					string(ref.UID),
					errMsg,
				})
				continue
			}

			if ref.UID != owner.GetUID() {
				table.Append([]string{
					fileName,
					ref.Kind,
					ref.Name,
					string(ref.UID),
					string(owner.GetUID()),
				})
				ref.UID = owner.GetUID()
				changed = true
			}
		}
		if changed {
			us.SetOwnerReferences(refs)
			err := write(config, file, us)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func findOwner(ctx context.Context, ac *client.ApiClient, owners map[string]*unstructured.Unstructured, ref *v1.OwnerReference, us *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	key := us.GetNamespace() + "#" + ref.APIVersion + "#" + ref.Name
	if owner, ok := owners[key]; ok {
		return owner, nil
	}

	gv, err := schema.ParseGroupVersion(ref.APIVersion)
	if err != nil {
		return nil, err
	}
	mapping, err := ac.Mapper.RESTMapping(schema.GroupKind{
		Group: gv.Group,
		Kind:  ref.Kind,
	}, gv.Version)
	if err != nil {
		return nil, err
	}
	owner, err := ac.Client.Resource(mapping.Resource).Namespace(us.GetNamespace()).Get(ctx, ref.Name, v1.GetOptions{})
	if err != nil {
		return nil, err
	}
	owners[key] = owner
	return owner, nil
}

func read(file string) (*unstructured.Unstructured, error) {
	us := &unstructured.Unstructured{}

	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	decoder := yaml.NewYAMLOrJSONDecoder(bufio.NewReader(f), 20)
	err = decoder.Decode(us)
	if err != nil {
		return nil, err
	}
	return us, nil
}

func write(config *types.Config, file string, us *unstructured.Unstructured) error {
	f, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return err
	}
	defer f.Close()
	err = config.PrintObj(us, f)
	if err != nil {
		return err
	}
	return nil
}