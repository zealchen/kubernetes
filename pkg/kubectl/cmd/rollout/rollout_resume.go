/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package rollout

import (
	"fmt"

	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/kubernetes/pkg/api/legacyscheme"
	"k8s.io/kubernetes/pkg/kubectl"
	"k8s.io/kubernetes/pkg/kubectl/cmd/set"
	"k8s.io/kubernetes/pkg/kubectl/cmd/templates"
	cmdutil "k8s.io/kubernetes/pkg/kubectl/cmd/util"
	"k8s.io/kubernetes/pkg/kubectl/genericclioptions"
	"k8s.io/kubernetes/pkg/kubectl/resource"
	"k8s.io/kubernetes/pkg/kubectl/util/i18n"
	"k8s.io/kubernetes/pkg/printers"
)

// ResumeConfig is the start of the data required to perform the operation.  As new fields are added, add them here instead of
// referencing the cmd.Flags()
type ResumeConfig struct {
	resource.FilenameOptions
	PrintFlags *printers.PrintFlags
	ToPrinter  func(string) (printers.ResourcePrinterFunc, error)

	Resumer func(object *resource.Info) ([]byte, error)
	Mapper  meta.RESTMapper
	Infos   []*resource.Info

	genericclioptions.IOStreams
}

var (
	resume_long = templates.LongDesc(`
		Resume a paused resource

		Paused resources will not be reconciled by a controller. By resuming a
		resource, we allow it to be reconciled again.
		Currently only deployments support being resumed.`)

	resume_example = templates.Examples(`
		# Resume an already paused deployment
		kubectl rollout resume deployment/nginx`)
)

func NewCmdRolloutResume(f cmdutil.Factory, streams genericclioptions.IOStreams) *cobra.Command {
	o := &ResumeConfig{
		PrintFlags: printers.NewPrintFlags("resumed"),
		IOStreams:  streams,
	}

	validArgs := []string{"deployment"}
	argAliases := kubectl.ResourceAliases(validArgs)

	cmd := &cobra.Command{
		Use: "resume RESOURCE",
		DisableFlagsInUseLine: true,
		Short:   i18n.T("Resume a paused resource"),
		Long:    resume_long,
		Example: resume_example,
		Run: func(cmd *cobra.Command, args []string) {
			allErrs := []error{}
			err := o.CompleteResume(f, cmd, args)
			if err != nil {
				allErrs = append(allErrs, err)
			}
			err = o.RunResume()
			if err != nil {
				allErrs = append(allErrs, err)
			}
			cmdutil.CheckErr(utilerrors.Flatten(utilerrors.NewAggregate(allErrs)))
		},
		ValidArgs:  validArgs,
		ArgAliases: argAliases,
	}

	usage := "identifying the resource to get from a server."
	cmdutil.AddFilenameOptionFlags(cmd, &o.FilenameOptions, usage)
	return cmd
}

func (o *ResumeConfig) CompleteResume(f cmdutil.Factory, cmd *cobra.Command, args []string) error {
	if len(args) == 0 && cmdutil.IsFilenameSliceEmpty(o.Filenames) {
		return cmdutil.UsageErrorf(cmd, "%s", cmd.Use)
	}

	o.Mapper = f.RESTMapper()

	o.Resumer = f.Resumer

	cmdNamespace, enforceNamespace, err := f.DefaultNamespace()
	if err != nil {
		return err
	}

	o.ToPrinter = func(operation string) (printers.ResourcePrinterFunc, error) {
		o.PrintFlags.NamePrintFlags.Operation = operation
		printer, err := o.PrintFlags.ToPrinter()
		if err != nil {
			return nil, err
		}

		return printer.PrintObj, nil
	}

	r := f.NewBuilder().
		Internal(legacyscheme.Scheme).
		NamespaceParam(cmdNamespace).DefaultNamespace().
		FilenameParam(enforceNamespace, &o.FilenameOptions).
		ResourceTypeOrNameArgs(true, args...).
		ContinueOnError().
		Latest().
		Flatten().
		Do()
	err = r.Err()
	if err != nil {
		return err
	}

	err = r.Visit(func(info *resource.Info, err error) error {
		if err != nil {
			return err
		}
		o.Infos = append(o.Infos, info)
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

func (o ResumeConfig) RunResume() error {
	allErrs := []error{}
	for _, patch := range set.CalculatePatches(o.Infos, cmdutil.InternalVersionJSONEncoder(), o.Resumer) {
		info := patch.Info

		if patch.Err != nil {
			allErrs = append(allErrs, fmt.Errorf("error: %s %q %v", info.Mapping.Resource, info.Name, patch.Err))
			continue
		}

		if string(patch.Patch) == "{}" || len(patch.Patch) == 0 {
			printer, err := o.ToPrinter("already resumed")
			if err != nil {
				allErrs = append(allErrs, err)
				continue
			}
			printer.PrintObj(info.AsVersioned(legacyscheme.Scheme), o.Out)
		}

		obj, err := resource.NewHelper(info.Client, info.Mapping).Patch(info.Namespace, info.Name, types.StrategicMergePatchType, patch.Patch)
		if err != nil {
			allErrs = append(allErrs, fmt.Errorf("failed to patch: %v", err))
			continue
		}

		info.Refresh(obj, true)
		printer, err := o.ToPrinter("resumed")
		if err != nil {
			allErrs = append(allErrs, err)
			continue
		}
		printer.PrintObj(info.AsVersioned(legacyscheme.Scheme), o.Out)
	}

	return utilerrors.NewAggregate(allErrs)
}
