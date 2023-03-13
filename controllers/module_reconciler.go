/*
Copyright 2022.

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

package controllers

import (
	"context"
	"fmt"
	"strings"

	kmmv1beta1 "github.com/kubernetes-sigs/kernel-module-management/api/v1beta1"
	"github.com/kubernetes-sigs/kernel-module-management/internal/api"
	"github.com/kubernetes-sigs/kernel-module-management/internal/build"
	"github.com/kubernetes-sigs/kernel-module-management/internal/daemonset"
	"github.com/kubernetes-sigs/kernel-module-management/internal/filter"
	"github.com/kubernetes-sigs/kernel-module-management/internal/imgbuild"
	"github.com/kubernetes-sigs/kernel-module-management/internal/metrics"
	"github.com/kubernetes-sigs/kernel-module-management/internal/sign"
	"github.com/kubernetes-sigs/kernel-module-management/internal/statusupdater"
	"github.com/kubernetes-sigs/kernel-module-management/internal/utils"
	"github.com/mitchellh/hashstructure/v2"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const ModuleReconcilerName = "Module"

// ModuleReconciler reconciles a Module object
type ModuleReconciler struct {
	client.Client

	daemonAPI         daemonset.DaemonSetCreator
	operatorNamespace string
	filter            *filter.Filter
	statusUpdaterAPI  statusupdater.ModuleStatusUpdater
	reconHelperAPI    moduleReconcilerHelperAPI
}

func NewModuleReconciler(
	client client.Client,
	buildAPI build.Manager,
	signAPI sign.Manager,
	daemonAPI daemonset.DaemonSetCreator,
	kernelAPI api.ModuleLoaderDataFactory,
	metricsAPI metrics.Metrics,
	filter *filter.Filter,
	statusUpdaterAPI statusupdater.ModuleStatusUpdater,
	operatorNamespace string,
) *ModuleReconciler {
	reconHelperAPI := newModuleReconcilerHelper(client, buildAPI, signAPI, daemonAPI, kernelAPI, metricsAPI)
	return &ModuleReconciler{
		daemonAPI:         daemonAPI,
		reconHelperAPI:    reconHelperAPI,
		filter:            filter,
		statusUpdaterAPI:  statusUpdaterAPI,
		operatorNamespace: operatorNamespace,
	}
}

//+kubebuilder:rbac:groups=kmm.sigs.x-k8s.io,resources=modules,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=kmm.sigs.x-k8s.io,resources=modules/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=create;delete;get;list;patch;watch
//+kubebuilder:rbac:groups="core",resources=nodes,verbs=get;list;watch
//+kubebuilder:rbac:groups="core",resources=secrets,verbs=get;list;watch
//+kubebuilder:rbac:groups="core",resources=configmaps,verbs=get;list;watch
//+kubebuilder:rbac:groups="batch",resources=jobs,verbs=create;list;watch;delete

// Reconcile lists all nodes and looks for kernels that match its mappings.
// For each mapping that matches at least one node in the cluster, it creates a DaemonSet running the container image
// on the nodes with a compatible kernel.
func (r *ModuleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	res := ctrl.Result{}

	logger := log.FromContext(ctx)

	mod, err := r.reconHelperAPI.getRequestedModule(ctx, req.NamespacedName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("Module deleted")
			return ctrl.Result{}, nil
		}

		return res, fmt.Errorf("failed to get the requested %s KMMO CR: %w", req.NamespacedName, err)
	}

	r.reconHelperAPI.setKMMOMetrics(ctx)

	targetedNodes, err := r.reconHelperAPI.getNodesListBySelector(ctx, mod)
	if err != nil {
		return res, fmt.Errorf("could get targeted nodes for module %s: %w", mod.Name, err)
	}

	mldMappings, nodesWithMapping, err := r.reconHelperAPI.getRelevantKernelMappingsAndNodes(ctx, mod, targetedNodes)
	if err != nil {
		return res, fmt.Errorf("could get kernel mappings and nodes for modules %s: %w", mod.Name, err)
	}

	for kernelVersion, mld := range mldMappings {
		completedSuccessfully, err := r.reconHelperAPI.handleBuild(ctx, mld)
		if err != nil {
			return res, fmt.Errorf("failed to handle build for kernel version %s: %v", kernelVersion, err)
		}
		mldLogger := logger.WithValues(
			"kernel version", kernelVersion,
			"mld", mld,
		)
		if !completedSuccessfully {
			mldLogger.Info("Build has not finished successfully yet:skipping handling signing and driver container for now")
			continue
		}

		completedSuccessfully, err = r.reconHelperAPI.handleSigning(ctx, mld)
		if err != nil {
			return res, fmt.Errorf("failed to handle signing for kernel version %s: %v", kernelVersion, err)
		}
		if !completedSuccessfully {
			mldLogger.Info("Signing has not finished successfully yet; skipping handling driver container for now")
			continue
		}

		err = r.reconHelperAPI.handleDriverContainer(ctx, mld)
		if err != nil {
			return res, fmt.Errorf("failed to handle driver container for kernel version %s: %v", kernelVersion, err)
		}
	}

	logger.Info("Handle device plugin")
	err = r.reconHelperAPI.handleDevicePlugin(ctx, mod)
	if err != nil {
		return res, fmt.Errorf("could handle device plugin: %w", err)
	}

	existingModuleDS, err := r.daemonAPI.GetModuleDaemonSets(ctx, mod.Name, mod.Namespace)
	if err != nil {
		return res, fmt.Errorf("could get DaemonSets for module %s, namespace %s: %v", mod.Name, mod.Namespace, err)
	}

	logger.Info("Run garbage collection")
	err = r.reconHelperAPI.garbageCollect(ctx, mod, mldMappings, existingModuleDS)
	if err != nil {
		return res, fmt.Errorf("failed to run garbage collection: %v", err)
	}

	err = r.statusUpdaterAPI.ModuleUpdateStatus(ctx, mod, nodesWithMapping, targetedNodes, existingModuleDS)
	if err != nil {
		return res, fmt.Errorf("failed to update status of the module: %w", err)
	}

	logger.Info("Reconcile loop finished successfully")

	return res, nil
}

//go:generate mockgen -source=module_reconciler.go -package=controllers -destination=mock_module_reconciler.go moduleReconcilerHelperAPI

type moduleReconcilerHelperAPI interface {
	getRequestedModule(ctx context.Context, namespacedName types.NamespacedName) (*kmmv1beta1.Module, error)
	setKMMOMetrics(ctx context.Context)
	getNodesListBySelector(ctx context.Context, mod *kmmv1beta1.Module) ([]v1.Node, error)
	getRelevantKernelMappingsAndNodes(ctx context.Context, mod *kmmv1beta1.Module, targetedNodes []v1.Node) (map[string]*api.ModuleLoaderData, []v1.Node, error)
	handleBuild(ctx context.Context, mld *api.ModuleLoaderData) (bool, error)
	handleSigning(ctx context.Context, mld *api.ModuleLoaderData) (bool, error)
	handleDriverContainer(ctx context.Context, mld *api.ModuleLoaderData) error
	handleDevicePlugin(ctx context.Context, mod *kmmv1beta1.Module) error
	garbageCollect(ctx context.Context, mod *kmmv1beta1.Module, mldMappings map[string]*api.ModuleLoaderData, existingDS []appsv1.DaemonSet) error
}

type hashData struct {
	KernelVersion string
	ModuleVersion string
}

type moduleReconcilerHelper struct {
	client     client.Client
	buildAPI   build.Manager
	signAPI    sign.Manager
	daemonAPI  daemonset.DaemonSetCreator
	kernelAPI  api.ModuleLoaderDataFactory
	metricsAPI metrics.Metrics
}

func newModuleReconcilerHelper(client client.Client,
	buildAPI build.Manager,
	signAPI sign.Manager,
	daemonAPI daemonset.DaemonSetCreator,
	kernelAPI api.ModuleLoaderDataFactory,
	metricsAPI metrics.Metrics) moduleReconcilerHelperAPI {
	return &moduleReconcilerHelper{
		client:     client,
		buildAPI:   buildAPI,
		signAPI:    signAPI,
		daemonAPI:  daemonAPI,
		kernelAPI:  kernelAPI,
		metricsAPI: metricsAPI,
	}
}

func (mrh *moduleReconcilerHelper) getRelevantKernelMappingsAndNodes(ctx context.Context,
	mod *kmmv1beta1.Module,
	targetedNodes []v1.Node) (map[string]*api.ModuleLoaderData, []v1.Node, error) {

	mldMappings := make(map[string]*api.ModuleLoaderData)
	logger := log.FromContext(ctx)

	nodes := make([]v1.Node, 0, len(targetedNodes))

	for _, node := range targetedNodes {
		kernelVersion := strings.TrimSuffix(node.Status.NodeInfo.KernelVersion, "+")

		nodeLogger := logger.WithValues(
			"node", node.Name,
			"kernel version", kernelVersion,
		)

		if mld, ok := mldMappings[kernelVersion]; ok {
			nodes = append(nodes, node)
			nodeLogger.V(1).Info("Using cached mld mapping", "mld", mld)
			continue
		}

		mld, err := mrh.kernelAPI.FromModule(mod, kernelVersion)
		if err != nil {
			nodeLogger.Error(err, "failed to get and process kernel mapping")
			continue
		}

		nodeLogger.V(1).Info("Found a valid mapping",
			"image", mld.ContainerImage,
			"build", mld.Build != nil,
		)

		mldMappings[kernelVersion] = mld
		nodes = append(nodes, node)
	}
	return mldMappings, nodes, nil
}

func (mrh *moduleReconcilerHelper) getNodesListBySelector(ctx context.Context, mod *kmmv1beta1.Module) ([]v1.Node, error) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("Listing nodes", "selector", mod.Spec.Selector)

	selectedNodes := v1.NodeList{}
	opt := client.MatchingLabels(mod.Spec.Selector)
	if err := mrh.client.List(ctx, &selectedNodes, opt); err != nil {
		logger.Error(err, "Could not list nodes")
		return nil, fmt.Errorf("could not list nodes: %v", err)
	}
	nodes := make([]v1.Node, 0, len(selectedNodes.Items))

	for _, node := range selectedNodes.Items {
		if isNodeSchedulable(&node) {
			nodes = append(nodes, node)
		}
	}
	return nodes, nil
}

// handleBuild returns true if build is not needed or finished successfully
func (mrh *moduleReconcilerHelper) handleBuild(ctx context.Context, mld *api.ModuleLoaderData) (bool, error) {

	shouldSync, err := mrh.buildAPI.ShouldSync(ctx, mld)
	if err != nil {
		return false, fmt.Errorf("could not check if build synchronization is needed: %w", err)
	}
	if !shouldSync {
		return true, nil
	}

	logger := log.FromContext(ctx).WithValues("kernel version", mld.KernelVersion, "image", mld.ContainerImage)
	buildCtx := log.IntoContext(ctx, logger)

	buildStatus, err := mrh.buildAPI.Sync(buildCtx, mld, true, mld.Owner)
	if err != nil {
		return false, fmt.Errorf("could not synchronize the build: %w", err)
	}

	completedSuccessfully := false
	switch buildStatus {
	case imgbuild.StatusCompleted:
		completedSuccessfully = true
	case imgbuild.StatusFailed:
		logger.Info(utils.WarnString("Build job has failed. If the fix is not in Module CR, then delete job after the fix in order to restart the job"))
	}

	return completedSuccessfully, nil
}

// handleSigning returns true if signing is not needed or finished successfully
func (mrh *moduleReconcilerHelper) handleSigning(ctx context.Context, mld *api.ModuleLoaderData) (bool, error) {
	shouldSync, err := mrh.signAPI.ShouldSync(ctx, mld)
	if err != nil {
		return false, fmt.Errorf("cound not check if synchronization is needed: %w", err)
	}
	if !shouldSync {
		return true, nil
	}

	logger := log.FromContext(ctx).WithValues("kernel version", mld.KernelVersion, "image", mld.ContainerImage)
	signCtx := log.IntoContext(ctx, logger)

	signStatus, err := mrh.signAPI.Sync(signCtx, mld, true, mld.Owner)
	if err != nil {
		return false, fmt.Errorf("could not synchronize the signing: %w", err)
	}

	completedSuccessfully := false
	switch signStatus {
	case imgbuild.StatusCompleted:
		completedSuccessfully = true
	case imgbuild.StatusFailed:
		logger.Info(utils.WarnString("Sign job has failed. If the fix is not in Module CR, then delete job after the fix in order to restart the job"))
	}

	return completedSuccessfully, nil
}

func (mrh *moduleReconcilerHelper) handleDriverContainer(ctx context.Context,
	mld *api.ModuleLoaderData) error {

	dsNameData := hashData{
		KernelVersion: mld.KernelVersion,
		ModuleVersion: mld.ModuleVersion,
	}
	hashValue, err := hashstructure.Hash(dsNameData, hashstructure.FormatV2, nil)
	if err != nil {
		return fmt.Errorf("failed to hash kernel and module versions (%s %s) and for module loader daemonset name: %v", mld.KernelVersion, mld.ModuleVersion, err)
	}
	name := fmt.Sprintf("%s-%x", mld.Name, hashValue)
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: mld.Namespace, Name: name},
	}

	logger := log.FromContext(ctx)
	opRes, err := controllerutil.CreateOrPatch(ctx, mrh.client, ds, func() error {
		return mrh.daemonAPI.SetDriverContainerAsDesired(ctx, ds, mld)
	})

	if err == nil {
		logger.Info("Reconciled Driver Container", "name", ds.Name, "result", opRes)
	}

	return err
}

func (mrh *moduleReconcilerHelper) handleDevicePlugin(ctx context.Context, mod *kmmv1beta1.Module) error {
	if mod.Spec.DevicePlugin == nil {
		return nil
	}

	logger := log.FromContext(ctx)
	hashValue, err := hashstructure.Hash(hashData{ModuleVersion: mod.Spec.ModuleLoader.Container.Version}, hashstructure.FormatV2, nil)
	if err != nil {
		return fmt.Errorf("failed to hash module version %s for device-plugin daemonset name: %v", mod.Spec.ModuleLoader.Container.Version, err)
	}
	name := fmt.Sprintf("%s-device-plugin-%x", mod.Name, hashValue)
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: mod.Namespace},
	}

	opRes, err := controllerutil.CreateOrPatch(ctx, mrh.client, ds, func() error {
		return mrh.daemonAPI.SetDevicePluginAsDesired(ctx, ds, mod)
	})

	if err == nil {
		logger.Info("Reconciled Device Plugin", "name", ds.Name, "result", opRes)
	}

	return err
}

func (mrh *moduleReconcilerHelper) garbageCollect(ctx context.Context,
	mod *kmmv1beta1.Module,
	mldMappings map[string]*api.ModuleLoaderData,
	existingDS []appsv1.DaemonSet) error {
	logger := log.FromContext(ctx)
	// Garbage collect old DaemonSets for which there are no nodes.
	validKernels := sets.KeySet[string](mldMappings)

	deleted, err := mrh.daemonAPI.GarbageCollect(ctx, mod, existingDS, validKernels)
	if err != nil {
		return fmt.Errorf("could not garbage collect DaemonSets: %v", err)
	}

	logger.Info("Garbage-collected DaemonSets", "names", deleted)

	// Garbage collect for successfully finished build jobs
	deleted, err = mrh.buildAPI.GarbageCollect(ctx, mod.Name, mod.Namespace, mod)
	if err != nil {
		return fmt.Errorf("could not garbage collect build objects: %v", err)
	}

	logger.Info("Garbage-collected Build objects", "names", deleted)

	// Garbage collect for successfully finished sign jobs
	deleted, err = mrh.signAPI.GarbageCollect(ctx, mod.Name, mod.Namespace, mod)
	if err != nil {
		return fmt.Errorf("could not garbage collect sign objects: %v", err)
	}

	logger.Info("Garbage-collected Sign objects", "names", deleted)

	return nil
}

func (mrh *moduleReconcilerHelper) setKMMOMetrics(ctx context.Context) {
	logger := log.FromContext(ctx)

	mods := kmmv1beta1.ModuleList{}
	err := mrh.client.List(ctx, &mods)
	if err != nil {
		logger.V(1).Info("failed to list KMMomodules for metrics", "error", err)
		return
	}

	numModules := len(mods.Items)
	numModulesWithBuild := 0
	numModulesWithSign := 0
	numModulesWithDevicePlugin := 0
	for _, mod := range mods.Items {
		if mod.Spec.DevicePlugin != nil {
			numModulesWithDevicePlugin += 1
		}
		buildCapable, signCapable := isModuleBuildAndSignCapable(&mod)
		if buildCapable {
			numModulesWithBuild += 1
		}
		if signCapable {
			numModulesWithSign += 1
		}

		if mod.Spec.ModuleLoader.Container.Modprobe.Args != nil {
			modprobeArgs := strings.Join(mod.Spec.ModuleLoader.Container.Modprobe.Args.Load, ",")
			mrh.metricsAPI.SetKMMModprobeArgs(mod.Name, mod.Namespace, modprobeArgs)
		}
		if mod.Spec.ModuleLoader.Container.Modprobe.RawArgs != nil {
			modprobeRawArgs := strings.Join(mod.Spec.ModuleLoader.Container.Modprobe.RawArgs.Load, ",")
			mrh.metricsAPI.SetKMMModprobeRawArgs(mod.Name, mod.Namespace, modprobeRawArgs)
		}
	}
	mrh.metricsAPI.SetKMMModulesNum(numModules)
	mrh.metricsAPI.SetKMMInClusterBuildNum(numModulesWithBuild)
	mrh.metricsAPI.SetKMMInClusterSignNum(numModulesWithSign)
	mrh.metricsAPI.SetKMMDevicePluginNum(numModulesWithDevicePlugin)
}

func (mrh *moduleReconcilerHelper) getRequestedModule(ctx context.Context, namespacedName types.NamespacedName) (*kmmv1beta1.Module, error) {
	mod := kmmv1beta1.Module{}

	if err := mrh.client.Get(ctx, namespacedName, &mod); err != nil {
		return nil, fmt.Errorf("failed to get the kmmo module %s: %w", namespacedName, err)
	}
	return &mod, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ModuleReconciler) SetupWithManager(mgr ctrl.Manager, kernelLabel string) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kmmv1beta1.Module{}).
		Owns(&appsv1.DaemonSet{}).
		Owns(&batchv1.Job{}).
		Watches(
			&source.Kind{Type: &v1.Node{}},
			handler.EnqueueRequestsFromMapFunc(r.filter.FindModulesForNode),
			builder.WithPredicates(
				r.filter.ModuleReconcilerNodePredicate(kernelLabel),
			),
		).
		Named(ModuleReconcilerName).
		Complete(r)
}

func isNodeSchedulable(node *v1.Node) bool {
	for _, taint := range node.Spec.Taints {
		if taint.Effect == v1.TaintEffectNoSchedule {
			return false
		}
	}
	return true
}

func isModuleBuildAndSignCapable(mod *kmmv1beta1.Module) (bool, bool) {
	buildCapable := mod.Spec.ModuleLoader.Container.Build != nil
	signCapable := mod.Spec.ModuleLoader.Container.Sign != nil
	if buildCapable && signCapable {
		return true, true
	}
	for _, mapping := range mod.Spec.ModuleLoader.Container.KernelMappings {
		if mapping.Sign != nil {
			signCapable = true
		}
		if mapping.Build != nil {
			buildCapable = true
		}
	}
	return buildCapable, signCapable
}
