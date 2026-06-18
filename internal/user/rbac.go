package user

import (
	"context"
	"errors"
	"fmt"
	"strings"

	rbac "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// rbacBinder owns all RBAC operations for a user: cluster/namespace role
// bindings, role creation, and binding cleanup. It changes only when RBAC
// handling changes (SRP).
type rbacBinder struct {
	clientset kubernetes.Interface
}

func newRBACBinder(clientset kubernetes.Interface) *rbacBinder {
	return &rbacBinder{clientset: clientset}
}

// subjectsFor builds the User subject plus one Group subject per group.
func subjectsFor(username string, groups []string) []rbac.Subject {
	subjects := []rbac.Subject{
		{Kind: "User", Name: username, APIGroup: "rbac.authorization.k8s.io"},
	}
	for _, group := range groups {
		subjects = append(subjects, rbac.Subject{
			Kind: "Group", Name: group, APIGroup: "rbac.authorization.k8s.io",
		})
	}
	return subjects
}

// BindClusterRole binds username (and groups) to a ClusterRole cluster-wide.
func (b *rbacBinder) BindClusterRole(username string, groups []string, clusterRole string) error {
	binding := &rbac.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s-binding", username, clusterRole),
		},
		Subjects: subjectsFor(username, groups),
		RoleRef: rbac.RoleRef{
			Kind:     "ClusterRole",
			Name:     clusterRole,
			APIGroup: "rbac.authorization.k8s.io",
		},
	}

	_, err := b.clientset.RbacV1().ClusterRoleBindings().Create(
		context.TODO(), binding, metav1.CreateOptions{})
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			fmt.Printf("  ClusterRoleBinding already exists, skipping...\n")
			return nil
		}
		return fmt.Errorf("failed to create ClusterRoleBinding: %w", err)
	}

	return nil
}

// BindRole binds username (and groups) to a Role in a namespace.
func (b *rbacBinder) BindRole(username string, groups []string, role, namespace string) error {
	binding := &rbac.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s-binding", username, role),
			Namespace: namespace,
		},
		Subjects: subjectsFor(username, groups),
		RoleRef: rbac.RoleRef{
			Kind:     "Role",
			Name:     role,
			APIGroup: "rbac.authorization.k8s.io",
		},
	}

	_, err := b.clientset.RbacV1().RoleBindings(namespace).Create(
		context.TODO(), binding, metav1.CreateOptions{})
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			fmt.Printf("  RoleBinding already exists, skipping...\n")
			return nil
		}
		return fmt.Errorf("failed to create RoleBinding: %w", err)
	}

	return nil
}

// DeleteForUser removes all cluster and namespaced role bindings whose name is
// prefixed with "<username>-". It is best-effort across items but returns an
// aggregated error so callers can report partial failures instead of silently
// leaving orphaned bindings (which would keep the deleted user authorized).
func (b *rbacBinder) DeleteForUser(username string) error {
	var errs []error

	// Delete ClusterRoleBindings
	bindings, err := b.clientset.RbacV1().ClusterRoleBindings().List(
		context.TODO(), metav1.ListOptions{})
	if err != nil {
		errs = append(errs, fmt.Errorf("listing ClusterRoleBindings: %w", err))
	} else {
		for _, binding := range bindings.Items {
			if strings.HasPrefix(binding.Name, username+"-") {
				fmt.Printf("  Deleting ClusterRoleBinding: %s\n", binding.Name)
				if err := b.clientset.RbacV1().ClusterRoleBindings().Delete(
					context.TODO(), binding.Name, metav1.DeleteOptions{}); err != nil {
					errs = append(errs, fmt.Errorf("deleting ClusterRoleBinding %s: %w", binding.Name, err))
				}
			}
		}
	}

	// Delete RoleBindings in all namespaces
	namespaces, err := b.clientset.CoreV1().Namespaces().List(
		context.TODO(), metav1.ListOptions{})
	if err != nil {
		errs = append(errs, fmt.Errorf("listing namespaces: %w", err))
	} else {
		for _, ns := range namespaces.Items {
			roleBindings, err := b.clientset.RbacV1().RoleBindings(ns.Name).List(
				context.TODO(), metav1.ListOptions{})
			if err != nil {
				errs = append(errs, fmt.Errorf("listing RoleBindings in %s: %w", ns.Name, err))
				continue
			}
			for _, rb := range roleBindings.Items {
				if strings.HasPrefix(rb.Name, username+"-") {
					fmt.Printf("  Deleting RoleBinding: %s/%s\n", ns.Name, rb.Name)
					if err := b.clientset.RbacV1().RoleBindings(ns.Name).Delete(
						context.TODO(), rb.Name, metav1.DeleteOptions{}); err != nil {
						errs = append(errs, fmt.Errorf("deleting RoleBinding %s/%s: %w", ns.Name, rb.Name, err))
					}
				}
			}
		}
	}

	return errors.Join(errs...)
}

// CreateRole creates a namespaced Role with the given policy rules.
func (b *rbacBinder) CreateRole(name, namespace string, rules []rbac.PolicyRule) error {
	role := &rbac.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Rules: rules,
	}

	_, err := b.clientset.RbacV1().Roles(namespace).Create(
		context.TODO(), role, metav1.CreateOptions{})
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			fmt.Printf("Role '%s' already exists in namespace '%s'\n", name, namespace)
			return nil
		}
		return fmt.Errorf("failed to create Role: %w", err)
	}

	fmt.Printf("Role '%s' created in namespace '%s'\n", name, namespace)
	return nil
}

// GetDefaultDeveloperRules returns common rules for developers.
func GetDefaultDeveloperRules() []rbac.PolicyRule {
	return []rbac.PolicyRule{
		{
			APIGroups: []string{"", "apps", "extensions", "batch"},
			Resources: []string{
				"pods", "pods/log", "pods/exec",
				"deployments", "replicasets", "statefulsets", "daemonsets",
				"services", "endpoints",
				"configmaps", "secrets",
				"jobs", "cronjobs",
				"persistentvolumeclaims",
			},
			Verbs: []string{"get", "list", "watch", "create", "update", "patch", "delete"},
		},
		{
			APIGroups: []string{"networking.k8s.io"},
			Resources: []string{"ingresses", "networkpolicies"},
			Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
		},
		{
			APIGroups: []string{"autoscaling"},
			Resources: []string{"horizontalpodautoscalers"},
			Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
		},
	}
}
