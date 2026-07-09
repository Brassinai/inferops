"""Tests for structural validation of rendered gateway exposure resources."""

from __future__ import annotations

from copy import deepcopy
import unittest

from scripts.check_gateway_exposure import validate_documents


def service(service_type: str = "ClusterIP") -> dict:
    spec = {
        "type": service_type,
        "selector": {"app": "gateway"},
        "ports": [{"name": "http", "port": 80, "targetPort": "http"}],
    }
    if service_type == "LoadBalancer":
        spec["externalTrafficPolicy"] = "Cluster"
    return {
        "apiVersion": "v1",
        "kind": "Service",
        "metadata": {"name": "inferops-gateway"},
        "spec": spec,
    }


def ingress() -> dict:
    return {
        "apiVersion": "networking.k8s.io/v1",
        "kind": "Ingress",
        "metadata": {"name": "inferops-gateway"},
        "spec": {
            "ingressClassName": "nginx",
            "rules": [
                {
                    "http": {
                        "paths": [
                            {
                                "path": "/",
                                "pathType": "Prefix",
                                "backend": {
                                    "service": {
                                        "name": "inferops-gateway",
                                        "port": {"name": "http"},
                                    }
                                },
                            }
                        ]
                    }
                }
            ],
        },
    }


def http_route() -> dict:
    return {
        "apiVersion": "gateway.networking.k8s.io/v1",
        "kind": "HTTPRoute",
        "metadata": {"name": "inferops-gateway"},
        "spec": {
            "parentRefs": [{"name": "public"}],
            "rules": [
                {
                    "matches": [{"path": {"type": "PathPrefix", "value": "/"}}],
                    "backendRefs": [
                        {
                            "group": "",
                            "kind": "Service",
                            "name": "inferops-gateway",
                            "port": 80,
                        }
                    ],
                }
            ],
        },
    }


def deployment(authenticated: bool = True) -> dict:
    env = [{"name": "POD_NAMESPACE"}]
    if authenticated:
        env.append({"name": "INFEROPS_GATEWAY_AUTH_TOKEN_FILE"})
    return {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": {"name": "inferops-gateway"},
        "spec": {
            "template": {
                "spec": {
                    "containers": [
                        {
                            "name": "gateway",
                            "env": env,
                        }
                    ]
                }
            }
        },
    }


class GatewayExposureValidationTest(unittest.TestCase):
    def test_accepts_portable_ingress_and_gateway_api_contracts(self) -> None:
        validate_documents(
            [service(), ingress()],
            "ingress",
            expected_class="nginx",
        )
        validate_documents([service(), http_route()], "gateway-api")
        validate_documents([service("LoadBalancer")], "load-balancer")

    def test_external_validation_can_require_gateway_authentication(self) -> None:
        validate_documents(
            [service(), ingress(), deployment()],
            "ingress",
            expected_class="nginx",
            require_auth=True,
        )
        with self.assertRaisesRegex(ValueError, "enable gateway authentication"):
            validate_documents(
                [service(), ingress(), deployment(authenticated=False)],
                "ingress",
                expected_class="nginx",
                require_auth=True,
            )

    def test_rejects_ingress_that_drops_model_subpaths(self) -> None:
        invalid = deepcopy(ingress())
        invalid["spec"]["rules"][0]["http"]["paths"][0]["pathType"] = "Exact"

        with self.assertRaisesRegex(ValueError, "Prefix"):
            validate_documents(
                [service(), invalid],
                "ingress",
                expected_class="nginx",
            )

    def test_rejects_httproute_to_the_wrong_backend(self) -> None:
        invalid = deepcopy(http_route())
        invalid["spec"]["rules"][0]["backendRefs"][0]["name"] = "other-service"

        with self.assertRaisesRegex(ValueError, "rendered gateway Service"):
            validate_documents([service(), invalid], "gateway-api")


if __name__ == "__main__":
    unittest.main()
