package fingerprintruntime

import (
	"errors"
	"reflect"
	"strings"
	"sync"
)

var ErrTemplateNotRegistered = errors.New("fingerprint runtime template is not registered")

// Injector is a concurrency-safe registry that materializes immutable runtime
// extensions. Launchers consume Artifact after Inject and append its Directory
// to their existing extension list.
type Injector struct {
	root string

	mu        sync.RWMutex
	templates map[string]ExtensionTemplate
	artifacts map[string]GeneratedExtension
}

func NewInjector(root ...string) *Injector {
	outputRoot := ""
	if len(root) != 0 {
		outputRoot = strings.TrimSpace(root[0])
	}
	return &Injector{
		root: outputRoot, templates: make(map[string]ExtensionTemplate), artifacts: make(map[string]GeneratedExtension),
	}
}

func (i *Injector) Register(template ExtensionTemplate) error {
	if i == nil {
		return ErrInvalidExtensionTemplate
	}
	if err := validateExtensionTemplate(template); err != nil {
		return err
	}
	template = cloneExtensionTemplate(template)
	i.mu.Lock()
	i.templates[template.ID] = template
	delete(i.artifacts, template.ID)
	i.mu.Unlock()
	return nil
}

// Inject prepares the registered template for Chromium. It keeps the original
// string-based API while replacing the former no-op implementation.
func (i *Injector) Inject(templateID string) error {
	if i == nil {
		return ErrTemplateNotRegistered
	}
	templateID = strings.TrimSpace(templateID)
	i.mu.RLock()
	template, exists := i.templates[templateID]
	root := i.root
	i.mu.RUnlock()
	if !exists {
		return ErrTemplateNotRegistered
	}
	artifact, err := WriteMV3Extension(root, template)
	if err != nil {
		return err
	}
	i.mu.Lock()
	// Do not publish a stale artifact if the template was replaced while the
	// content-addressed files were being written.
	current, stillExists := i.templates[templateID]
	if stillExists && extensionTemplatesEqual(current, template) {
		i.artifacts[templateID] = artifact
	}
	i.mu.Unlock()
	if !stillExists || !extensionTemplatesEqual(current, template) {
		return errors.New("fingerprint runtime template changed during injection")
	}
	return nil
}

func (i *Injector) Artifact(templateID string) (GeneratedExtension, bool) {
	if i == nil {
		return GeneratedExtension{}, false
	}
	i.mu.RLock()
	artifact, exists := i.artifacts[strings.TrimSpace(templateID)]
	i.mu.RUnlock()
	return artifact, exists
}

func cloneExtensionTemplate(template ExtensionTemplate) ExtensionTemplate {
	template.Configuration.Fonts = append([]string(nil), template.Configuration.Fonts...)
	if template.Configuration.MaxTouchPoints != nil {
		value := *template.Configuration.MaxTouchPoints
		template.Configuration.MaxTouchPoints = &value
	}
	if template.Configuration.MediaDevices != nil {
		value := *template.Configuration.MediaDevices
		template.Configuration.MediaDevices = &value
	}
	if template.Configuration.Battery != nil {
		value := *template.Configuration.Battery
		template.Configuration.Battery = &value
	}
	return template
}

func extensionTemplatesEqual(left, right ExtensionTemplate) bool {
	return reflect.DeepEqual(left, right)
}
