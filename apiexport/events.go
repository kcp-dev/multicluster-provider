/*
Copyright 2025 The KCP Authors.

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

package apiexport

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
)

// createEventBroadcaster creates a new event broadcaster from a rest.Config.
func createEventBroadcaster(config *rest.Config, defaultNamespace string) (record.EventRecorder, error) {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	eventBroadcaster := record.NewBroadcaster()

	eventBroadcaster.StartRecordingToSink(
		&typedcorev1.EventSinkImpl{Interface: clientset.CoreV1().Events("")},
	)

	baseRecorder := eventBroadcaster.NewRecorder(nil, corev1.EventSource{})

	return &namespacedEventRecorder{
		recorder:         baseRecorder,
		clientset:        clientset,
		defaultNamespace: defaultNamespace,
	}, nil
}

// namespacedEventRecorder wraps an EventRecorder to resolve namespaces dynamically
type namespacedEventRecorder struct {
	recorder         record.EventRecorder
	clientset        kubernetes.Interface
	defaultNamespace string
}

// extractNamespace extracts namespace from object or returns default
func (r *namespacedEventRecorder) extractNamespace(object runtime.Object) string {
	if object == nil {
		return r.defaultNamespace
	}

	if obj, ok := object.(metav1.Object); ok {
		if ns := obj.GetNamespace(); ns != "" {
			return ns
		}
	}

	return r.defaultNamespace
}

// Event creates an event in the appropriate namespace
func (r *namespacedEventRecorder) Event(object runtime.Object, eventtype, reason, message string) {
	namespace := r.extractNamespace(object)

	// Create a namespace-scoped event recorder for this specific event
	eventInterface := r.clientset.CoreV1().Events(namespace)
	eventSink := &typedcorev1.EventSinkImpl{Interface: eventInterface}

	// Create the event directly using the event sink
	if event := r.createEvent(object, eventtype, reason, message, namespace); event != nil {
		if _, err := eventSink.Create(event); err != nil {
			// Fallback to original recorder if creation fails
			r.recorder.Event(object, eventtype, reason, message)
		}
	}
}

// Eventf creates a formatted event in the appropriate namespace
func (r *namespacedEventRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
	message := fmt.Sprintf(messageFmt, args...)
	r.Event(object, eventtype, reason, message)
}

// AnnotatedEventf creates an annotated event in the appropriate namespace
func (r *namespacedEventRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
	namespace := r.extractNamespace(object)

	// Create a namespace-scoped event recorder for this specific event
	eventInterface := r.clientset.CoreV1().Events(namespace)
	eventSink := &typedcorev1.EventSinkImpl{Interface: eventInterface}

	message := fmt.Sprintf(messageFmt, args...)

	// Create the event with annotations
	if event := r.createEvent(object, eventtype, reason, message, namespace); event != nil {
		if event.Annotations == nil {
			event.Annotations = make(map[string]string)
		}
		for k, v := range annotations {
			event.Annotations[k] = v
		}

		if _, err := eventSink.Create(event); err != nil {
			// Fallback to original recorder if creation fails
			r.recorder.AnnotatedEventf(object, annotations, eventtype, reason, messageFmt, args...)
		}
	}
}

// createEvent creates a basic event object
func (r *namespacedEventRecorder) createEvent(object runtime.Object, eventtype, reason, message, namespace string) *corev1.Event {
	if object == nil {
		return nil
	}

	obj, ok := object.(metav1.Object)
	if !ok {
		return nil
	}

	return &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    namespace,
			GenerateName: "event-",
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:            object.GetObjectKind().GroupVersionKind().Kind,
			APIVersion:      object.GetObjectKind().GroupVersionKind().GroupVersion().String(),
			Name:            obj.GetName(),
			Namespace:       obj.GetNamespace(),
			UID:             obj.GetUID(),
			ResourceVersion: obj.GetResourceVersion(),
		},
		Reason:  reason,
		Message: message,
		Type:    eventtype,
		Source:  corev1.EventSource{},
	}
}
