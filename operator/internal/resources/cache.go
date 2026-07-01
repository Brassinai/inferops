package resources

import (
	"errors"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/internal/templates"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const defaultModelRevision = "main"

// BuildCacheDownloaderJob returns a deterministic Job that prepares a
// ModelCache on its selected node. Credentials are read from a Secret and are
// never copied into the Job's literal environment.
func (b Builder) BuildCacheDownloaderJob(cache *v1alpha1.ModelCache) (*batchv1.Job, error) {
	if cache == nil {
		return nil, errors.New("model cache is required")
	}
	if err := validateModelCacheName(cache.Name); err != nil {
		return nil, err
	}
	if cache.Spec.ModelRepo == "" {
		return nil, errors.New("model cache repository is required")
	}
	if cache.Spec.Storage.NodeName == "" {
		return nil, errors.New("model cache node is required before building a downloader Job")
	}
	if err := b.validateCachePath(cache.Spec.Storage.Path); err != nil {
		return nil, err
	}

	revision := cache.Spec.Revision
	if revision == "" {
		revision = defaultModelRevision
	}
	command := []string{
		"hf",
		"download",
		cache.Spec.ModelRepo,
		"--revision",
		revision,
		"--local-dir",
		b.runtimeModelPath,
	}
	container := corev1.Container{
		Name:            templates.CacheDownloaderContainerName,
		Image:           b.cacheDownloaderImage,
		ImagePullPolicy: imagePullPolicy(b.cacheDownloaderImage),
		Command:         command,
		Resources:       *b.cacheDownloaderResources.DeepCopy(),
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: boolPointer(false),
		},
	}
	if cache.Spec.SecretRef != "" {
		container.Env = []corev1.EnvVar{
			{
				Name: "HF_TOKEN",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: cache.Spec.SecretRef,
						},
						Key: "token",
					},
				},
			},
		}
	}

	volume, mount := b.cacheVolumeAndMount(cache.Spec.Storage.Path, false)
	container.VolumeMounts = []corev1.VolumeMount{mount}
	automountServiceAccountToken := false
	backoffLimit := int32(3)
	labels := CacheLabels(cache.Name)

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:            cache.Name + templates.CacheDownloaderJobSuffix,
			Namespace:       cache.Namespace,
			Labels:          labels,
			OwnerReferences: []metav1.OwnerReference{OwnerReferenceForModelCache(cache)},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: &automountServiceAccountToken,
					RestartPolicy:                corev1.RestartPolicyNever,
					NodeSelector: map[string]string{
						corev1.LabelHostname: cache.Spec.Storage.NodeName,
					},
					Containers: []corev1.Container{container},
					Volumes:    []corev1.Volume{volume},
				},
			},
		},
	}, nil
}

// CacheVolumeAndMount returns the hostPath volume and in-container mount used
// for a prepared cache. The host path must be under the configured cache root.
func (b Builder) CacheVolumeAndMount(
	cacheHostPath string,
	readOnly bool,
) (corev1.Volume, corev1.VolumeMount, error) {
	if err := b.validateCachePath(cacheHostPath); err != nil {
		return corev1.Volume{}, corev1.VolumeMount{}, err
	}
	volume, mount := b.cacheVolumeAndMount(cacheHostPath, readOnly)
	return volume, mount, nil
}

func (b Builder) cacheVolumeAndMount(
	cacheHostPath string,
	readOnly bool,
) (corev1.Volume, corev1.VolumeMount) {
	hostPathType := corev1.HostPathDirectory
	if !readOnly {
		hostPathType = corev1.HostPathDirectoryOrCreate
	}
	volume := corev1.Volume{
		Name: templates.CacheVolumeName,
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{
				Path: cacheHostPath,
				Type: &hostPathType,
			},
		},
	}
	mount := corev1.VolumeMount{
		Name:      templates.CacheVolumeName,
		MountPath: b.runtimeModelPath,
		ReadOnly:  readOnly,
	}
	return volume, mount
}

// NodeAffinityForCacheNode returns required node affinity for the node
// containing a ready cache.
func NodeAffinityForCacheNode(cacheNode string) *corev1.NodeAffinity {
	if cacheNode == "" {
		return nil
	}
	return &corev1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				{
					MatchExpressions: []corev1.NodeSelectorRequirement{
						{
							Key:      corev1.LabelHostname,
							Operator: corev1.NodeSelectorOpIn,
							Values:   []string{cacheNode},
						},
					},
				},
			},
		},
	}
}

func boolPointer(value bool) *bool {
	return &value
}
