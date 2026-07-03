package resources

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	pathpkg "path"
	"sort"
	"strings"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/internal/paths"
	"github.com/brassinai/inferops/operator/internal/templates"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	defaultModelRevision = "main"

	// CacheInputHashAnnotation is the Job annotation that records the hashed
	// artifact identity written into the completion marker.
	CacheInputHashAnnotation = "inferops.dev/cache-input-hash"

	// CacheJobHashAnnotation records execution inputs used to decide whether
	// an unfinished downloader Job must be recreated.
	CacheJobHashAnnotation = "inferops.dev/cache-job-hash"

	// CacheRetryAnnotation is a user-supplied token. Changing its value causes
	// exactly one new downloader Job attempt.
	CacheRetryAnnotation = "inferops.dev/retry"

	// CacheRetryTokenAnnotation records the retry token handled by a Job.
	CacheRetryTokenAnnotation = "inferops.dev/cache-retry-token"

	// CacheTTLSecondsAfterFinished controls how long a completed downloader Job
	// remains before garbage collection. It is long enough for the controller
	// to observe completion after a restart.
	cacheTTLSecondsAfterFinished = int32(3600)

	// CacheActiveDeadlineSeconds bounds the total wall time of a download
	// attempt. Large models may need tuning through operator configuration.
	cacheActiveDeadlineSeconds = int64(7200)

	// CacheBackoffLimit bounds per-attempt retries within one Job.
	cacheBackoffLimit = int32(2)

	cacheStagingDirectory = ".inferops-staging"
)

// CachePlacement carries a resolved cache destination into the Job builder.
type CachePlacement struct {
	NodeName     string
	NodeUID      string
	Path         string
	ReservedSize resource.Quantity
}

// BuildCacheDownloaderJob returns a deterministic Job that prepares a
// ModelCache on its selected node. Credentials are read from a Secret and are
// never copied into the Job's literal environment.
func (b Builder) BuildCacheDownloaderJob(
	cache *v1alpha1.ModelCache,
	placement CachePlacement,
) (*batchv1.Job, error) {
	if cache == nil {
		return nil, errors.New("model cache is required")
	}
	if err := validateModelCacheName(cache.Name); err != nil {
		return nil, err
	}
	if cache.Namespace == "" {
		return nil, errors.New("model cache namespace is required")
	}
	if cache.UID == "" {
		return nil, errors.New("model cache UID is required")
	}

	source := "huggingface"
	repo := cache.Spec.ModelRepo
	revision := cache.Spec.Revision
	secretRef := cache.Spec.SecretRef
	if repo == "" {
		return nil, errors.New("model repository is required")
	}
	if placement.NodeName == "" {
		return nil, errors.New("cache placement node is required before building a downloader Job")
	}
	if placement.NodeUID == "" {
		return nil, errors.New("cache placement node UID is required before building a downloader Job")
	}
	if placement.Path == "" {
		return nil, errors.New("cache placement path is required before building a downloader Job")
	}
	if err := b.validateCachePath(placement.Path); err != nil {
		return nil, err
	}
	if placement.ReservedSize.Sign() <= 0 {
		return nil, errors.New("cache placement reserved size must be greater than zero")
	}

	revision = effectiveRevision(revision)

	inputHash, err := cacheInputHash(cache, placement)
	if err != nil {
		return nil, fmt.Errorf("compute cache input hash: %w", err)
	}
	jobHash := cacheJobHash(
		inputHash,
		secretRef,
		b.cacheDownloaderImage,
		cache.Spec.Storage.NodeSelector,
		cache.Spec.Storage.Tolerations,
	)

	destSubpath, err := b.cacheSubpath(placement.Path)
	if err != nil {
		return nil, err
	}
	stagingSubpath := pathpkg.Join(
		cacheStagingDirectory,
		cache.Namespace,
		string(cache.UID),
	)

	container, err := b.buildDownloaderContainer(source, repo, revision, secretRef, destSubpath, stagingSubpath, inputHash)
	if err != nil {
		return nil, err
	}

	volumes := b.buildDownloaderVolumes()
	container.VolumeMounts = []corev1.VolumeMount{
		{
			Name:      templates.CacheVolumeName,
			MountPath: downloaderCacheRootMount,
		},
		{
			Name:      downloaderTemporaryVolume,
			MountPath: downloaderTemporaryMount,
		},
	}

	labels := CacheLabels(cache.Name)
	automountServiceAccountToken := false
	enableServiceLinks := false
	ttl := cacheTTLSecondsAfterFinished
	deadline := cacheActiveDeadlineSeconds
	backoff := cacheBackoffLimit

	annotations := map[string]string{
		CacheInputHashAnnotation: inputHash,
		CacheJobHashAnnotation:   jobHash,
	}
	if retryToken := cache.Annotations[CacheRetryAnnotation]; retryToken != "" {
		annotations[CacheRetryTokenAnnotation] = retryToken
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:            cache.Name + templates.CacheDownloaderJobSuffix,
			Namespace:       cache.Namespace,
			Labels:          labels,
			Annotations:     annotations,
			OwnerReferences: []metav1.OwnerReference{OwnerReferenceForModelCache(cache)},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			ActiveDeadlineSeconds:   &deadline,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
					Annotations: map[string]string{
						CacheInputHashAnnotation: inputHash,
					},
				},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: &automountServiceAccountToken,
					EnableServiceLinks:           &enableServiceLinks,
					RestartPolicy:                corev1.RestartPolicyNever,
					NodeSelector: mergeCacheNodeSelector(
						cache.Spec.Storage.NodeSelector,
						placement.NodeName,
					),
					Tolerations: buildTolerations(cache.Spec.Storage.Tolerations),
					Containers:  []corev1.Container{container},
					Volumes:     volumes,
				},
			},
		},
	}, nil
}

func mergeCacheNodeSelector(input map[string]string, nodeName string) map[string]string {
	result := copyStringMap(input)
	if result == nil {
		result = make(map[string]string, 1)
	}
	result[corev1.LabelHostname] = nodeName
	return result
}

const downloaderCacheRootMount = "/cache"

const (
	downloaderTemporaryVolume = "tmp"
	downloaderTemporaryMount  = "/tmp"
)

func (b Builder) buildDownloaderContainer(
	source, repo, revision, secretRef, destSubpath, stagingSubpath, inputHash string,
) (corev1.Container, error) {
	command := []string{
		"python",
		"/opt/inferops/download.py",
		"--source",
		source,
		"--repo",
		repo,
		"--revision",
		revision,
		"--cache-root",
		downloaderCacheRootMount,
		"--dest-subpath",
		destSubpath,
		"--staging-subpath",
		stagingSubpath,
		"--input-hash",
		inputHash,
	}

	container := corev1.Container{
		Name:            templates.CacheDownloaderContainerName,
		Image:           b.cacheDownloaderImage,
		ImagePullPolicy: imagePullPolicy(b.cacheDownloaderImage),
		Command:         command,
		Env: []corev1.EnvVar{
			{Name: "TMPDIR", Value: downloaderTemporaryMount},
		},
		Resources: *b.cacheDownloaderResources.DeepCopy(),
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: boolPointer(false),
			ReadOnlyRootFilesystem:   boolPointer(true),
			RunAsNonRoot:             boolPointer(true),
			RunAsUser:                int64Pointer(65532),
			RunAsGroup:               int64Pointer(65532),
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
			},
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		},
	}

	if secretRef != "" {
		container.Env = append(container.Env, corev1.EnvVar{
			Name: "HF_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: secretRef,
					},
					Key: "token",
				},
			},
		})
	}

	return container, nil
}

func (b Builder) buildDownloaderVolumes() []corev1.Volume {
	hostPathType := corev1.HostPathDirectory
	return []corev1.Volume{
		{
			Name: templates.CacheVolumeName,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: b.cacheRoot,
					Type: &hostPathType,
				},
			},
		},
		{
			Name: downloaderTemporaryVolume,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	}
}

func (b Builder) cacheSubpath(cachePath string) (string, error) {
	subpath, err := paths.ChildOfRoot(cachePath, b.cacheRoot)
	if err != nil {
		return "", err
	}
	if subpath == cacheStagingDirectory || strings.HasPrefix(subpath, cacheStagingDirectory+"/") {
		return "", fmt.Errorf("cache path %q uses reserved staging directory %q", cachePath, cacheStagingDirectory)
	}
	return subpath, nil
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

// CacheInputHash computes the deterministic identity of a cache artifact and
// its physical placement.
func CacheInputHash(
	cache *v1alpha1.ModelCache,
	placement CachePlacement,
) (string, error) {
	return cacheInputHash(cache, placement)
}

func cacheInputHash(
	cache *v1alpha1.ModelCache,
	placement CachePlacement,
) (string, error) {
	if cache == nil {
		return "", errors.New("model cache is required")
	}
	data := map[string]string{
		"source":          "huggingface",
		"repo":            cache.Spec.ModelRepo,
		"revision":        effectiveRevision(cache.Spec.Revision),
		"destinationPath": placement.Path,
		"nodeName":        placement.NodeName,
		"nodeUID":         placement.NodeUID,
		"reservedSize":    placement.ReservedSize.String(),
	}

	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	hash := sha256.New()
	for _, key := range keys {
		if _, err := hash.Write([]byte(key + "=" + data[key] + "\n")); err != nil {
			return "", err
		}
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func cacheJobHash(
	inputHash, secretRef, downloaderImage string,
	nodeSelector map[string]string,
	tolerations []v1alpha1.Toleration,
) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("inputHash=" + inputHash + "\n"))
	_, _ = hash.Write([]byte("secretRef=" + secretRef + "\n"))
	_, _ = hash.Write([]byte("downloaderImage=" + downloaderImage + "\n"))
	selectorKeys := make([]string, 0, len(nodeSelector))
	for key := range nodeSelector {
		selectorKeys = append(selectorKeys, key)
	}
	sort.Strings(selectorKeys)
	for _, key := range selectorKeys {
		_, _ = hash.Write([]byte("nodeSelector." + key + "=" + nodeSelector[key] + "\n"))
	}
	for i := range tolerations {
		seconds := ""
		if tolerations[i].TolerationSeconds != nil {
			seconds = fmt.Sprintf("%d", *tolerations[i].TolerationSeconds)
		}
		_, _ = fmt.Fprintf(
			hash,
			"toleration.%d=%s|%s|%s|%s|%s\n",
			i,
			tolerations[i].Key,
			tolerations[i].Operator,
			tolerations[i].Value,
			tolerations[i].Effect,
			seconds,
		)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

// CacheInputHashFromJob extracts the input hash from a downloader Job.
func CacheInputHashFromJob(job *batchv1.Job) string {
	if job == nil {
		return ""
	}
	if job.Annotations != nil {
		return job.Annotations[CacheInputHashAnnotation]
	}
	return ""
}

// CacheJobHashFromJob extracts the execution hash from a downloader Job.
func CacheJobHashFromJob(job *batchv1.Job) string {
	if job == nil || job.Annotations == nil {
		return ""
	}
	return job.Annotations[CacheJobHashAnnotation]
}

// JobStatus summarizes the observed state of a downloader Job.
type JobStatus struct {
	Active         int32
	Succeeded      int32
	Failed         int32
	Complete       bool
	FailedTerminal bool
	Message        string
}

// ObserveDownloaderJob returns a concise status from a Job. A Job is
// considered terminally failed only when it has exhausted its backoff.
func ObserveDownloaderJob(job *batchv1.Job) JobStatus {
	if job == nil {
		return JobStatus{}
	}
	status := JobStatus{
		Active:    job.Status.Active,
		Succeeded: job.Status.Succeeded,
		Failed:    job.Status.Failed,
	}
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobComplete && condition.Status == corev1.ConditionTrue {
			status.Complete = true
		}
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			status.FailedTerminal = true
			status.Message = condition.Message
			if status.Message == "" {
				status.Message = condition.Reason
			}
		}
	}
	return status
}

func effectiveRevision(revision string) string {
	if revision == "" {
		return defaultModelRevision
	}
	return revision
}

func boolPointer(value bool) *bool {
	return &value
}

func int64Pointer(value int64) *int64 {
	return &value
}
