package admission

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	v1alpha1 "github.com/brassinai/inferops/operator/api/v1alpha1"
	"github.com/brassinai/inferops/operator/internal/validation"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	cradmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	// ModelDeploymentPath is the validating admission endpoint for ModelDeployment.
	ModelDeploymentPath = "/validate-inference-inferops-dev-v1alpha1-modeldeployment"
	// ModelRuntimePath is the validating admission endpoint for ModelRuntime.
	ModelRuntimePath = "/validate-inference-inferops-dev-v1alpha1-modelruntime"
	// ModelCachePath is the validating admission endpoint for ModelCache.
	ModelCachePath = "/validate-inference-inferops-dev-v1alpha1-modelcache"
)

type objectValidator func(runtime.Object) error

type validationHandler struct {
	decoder   cradmission.Decoder
	newObject func() runtime.Object
	validate  objectValidator
}

// RegisterValidationWebhooks registers static validators for every InferOps
// custom resource served by v1alpha1.
func RegisterValidationWebhooks(server webhook.Server, scheme *runtime.Scheme) error {
	if server == nil {
		return errors.New("webhook server is required")
	}
	if scheme == nil {
		return errors.New("scheme is required")
	}
	decoder := cradmission.NewDecoder(scheme)
	server.Register(ModelDeploymentPath, validatingWebhook(newModelDeploymentHandler(decoder)))
	server.Register(ModelRuntimePath, validatingWebhook(newModelRuntimeHandler(decoder)))
	server.Register(ModelCachePath, validatingWebhook(newModelCacheHandler(decoder)))
	return nil
}

func validatingWebhook(handler cradmission.Handler) *cradmission.Webhook {
	return &cradmission.Webhook{
		Handler:      handler,
		RecoverPanic: true,
	}
}

func newModelDeploymentHandler(decoder cradmission.Decoder) cradmission.Handler {
	return &validationHandler{
		decoder:   decoder,
		newObject: func() runtime.Object { return &v1alpha1.ModelDeployment{} },
		validate: func(object runtime.Object) error {
			deployment, ok := object.(*v1alpha1.ModelDeployment)
			if !ok {
				return fmt.Errorf("decoded object is %T, expected *v1alpha1.ModelDeployment", object)
			}
			return validation.ValidateModelDeployment(*deployment)
		},
	}
}

func newModelRuntimeHandler(decoder cradmission.Decoder) cradmission.Handler {
	return &validationHandler{
		decoder:   decoder,
		newObject: func() runtime.Object { return &v1alpha1.ModelRuntime{} },
		validate: func(object runtime.Object) error {
			modelRuntime, ok := object.(*v1alpha1.ModelRuntime)
			if !ok {
				return fmt.Errorf("decoded object is %T, expected *v1alpha1.ModelRuntime", object)
			}
			return validation.ValidateModelRuntime(*modelRuntime)
		},
	}
}

func newModelCacheHandler(decoder cradmission.Decoder) cradmission.Handler {
	return &validationHandler{
		decoder:   decoder,
		newObject: func() runtime.Object { return &v1alpha1.ModelCache{} },
		validate: func(object runtime.Object) error {
			cache, ok := object.(*v1alpha1.ModelCache)
			if !ok {
				return fmt.Errorf("decoded object is %T, expected *v1alpha1.ModelCache", object)
			}
			return validation.ValidateModelCache(*cache)
		},
	}
}

func (h *validationHandler) Handle(_ context.Context, request cradmission.Request) cradmission.Response {
	switch request.Operation {
	case admissionv1.Create, admissionv1.Update:
	case admissionv1.Delete:
		return cradmission.Allowed("deletion does not require spec validation")
	default:
		return cradmission.Denied(fmt.Sprintf("admission operation %q is not supported", request.Operation))
	}
	if h == nil || h.decoder == nil || h.newObject == nil || h.validate == nil {
		return cradmission.Errored(http.StatusInternalServerError, errors.New("validation handler is not configured"))
	}

	object := h.newObject()
	if err := h.decoder.Decode(request, object); err != nil {
		return cradmission.Errored(http.StatusBadRequest, fmt.Errorf("decode admission object: %w", err))
	}
	if err := h.validate(object); err != nil {
		return cradmission.Denied(err.Error())
	}
	return cradmission.Allowed("InferOps resource passed admission validation")
}
