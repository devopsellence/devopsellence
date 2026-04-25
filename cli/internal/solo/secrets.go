package solo

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type ScopedSecrets map[string]map[string]string

func (s ScopedSecrets) ValuesForService(serviceName string) map[string]string {
	values := s[strings.TrimSpace(serviceName)]
	if values == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(values))
	for name, value := range values {
		out[name] = value
	}
	return out
}

func (s ScopedSecrets) Value(serviceName, name string) string {
	values := s[strings.TrimSpace(serviceName)]
	if values == nil {
		return ""
	}
	return values[strings.TrimSpace(name)]
}

func (s ScopedSecrets) Set(serviceName, name, value string) {
	serviceName = strings.TrimSpace(serviceName)
	name = strings.TrimSpace(name)
	if serviceName == "" || name == "" {
		return
	}
	if s[serviceName] == nil {
		s[serviceName] = map[string]string{}
	}
	s[serviceName][name] = value
}

func (s *State) SetSecret(workspaceRoot, environment, serviceName, name, value string) (SecretRecord, error) {
	s.ensureDefaults()
	key, record, err := buildSecretRecord(workspaceRoot, environment, serviceName, name, value)
	if err != nil {
		return SecretRecord{}, err
	}
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.Secrets[key] = record
	return record, nil
}

func (s *State) DeleteSecret(workspaceRoot, environment, serviceName, name string) (SecretRecord, error) {
	s.ensureDefaults()
	key, record, err := buildSecretRecord(workspaceRoot, environment, serviceName, name, "")
	if err != nil {
		return SecretRecord{}, err
	}
	existing, ok := s.Secrets[key]
	if !ok {
		return SecretRecord{}, fmt.Errorf("secret %q for service %q in %s not found", record.Name, record.ServiceName, record.Environment)
	}
	delete(s.Secrets, key)
	return existing, nil
}

func (s *State) ListSecrets(workspaceRoot, environment, serviceName string) ([]SecretRecord, error) {
	s.ensureDefaults()
	workspaceKey, err := CanonicalWorkspaceKey(workspaceRoot)
	if err != nil {
		return nil, err
	}
	environment = defaultEnvironmentName(environment)
	serviceName = strings.TrimSpace(serviceName)
	records := []SecretRecord{}
	for _, record := range s.Secrets {
		if record.WorkspaceKey != workspaceKey || record.Environment != environment {
			continue
		}
		if serviceName != "" && record.ServiceName != serviceName {
			continue
		}
		record.Value = ""
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].ServiceName != records[j].ServiceName {
			return records[i].ServiceName < records[j].ServiceName
		}
		return records[i].Name < records[j].Name
	})
	return records, nil
}

func (s *State) ScopedSecretValues(workspaceRoot, environment string) (ScopedSecrets, error) {
	s.ensureDefaults()
	workspaceKey, err := CanonicalWorkspaceKey(workspaceRoot)
	if err != nil {
		return nil, err
	}
	environment = defaultEnvironmentName(environment)
	values := ScopedSecrets{}
	for _, record := range s.Secrets {
		if record.WorkspaceKey != workspaceKey || record.Environment != environment {
			continue
		}
		values.Set(record.ServiceName, record.Name, record.Value)
	}
	return values, nil
}

func buildSecretRecord(workspaceRoot, environment, serviceName, name, value string) (string, SecretRecord, error) {
	workspaceKey, err := CanonicalWorkspaceKey(workspaceRoot)
	if err != nil {
		return "", SecretRecord{}, err
	}
	environment = defaultEnvironmentName(environment)
	serviceName = strings.TrimSpace(serviceName)
	name = strings.TrimSpace(name)
	if serviceName == "" {
		return "", SecretRecord{}, fmt.Errorf("service name is required")
	}
	if name == "" {
		return "", SecretRecord{}, fmt.Errorf("secret name is required")
	}
	record := SecretRecord{
		WorkspaceRoot: strings.TrimSpace(workspaceRoot),
		WorkspaceKey:  workspaceKey,
		Environment:   environment,
		ServiceName:   serviceName,
		Name:          name,
		Value:         value,
	}
	if record.WorkspaceRoot == "" {
		record.WorkspaceRoot = workspaceKey
	}
	return secretKey(workspaceKey, environment, serviceName, name), record, nil
}

func secretKey(workspaceKey, environment, serviceName, name string) string {
	return strings.Join([]string{
		strings.TrimSpace(workspaceKey),
		defaultEnvironmentName(environment),
		strings.TrimSpace(serviceName),
		strings.TrimSpace(name),
	}, "\n")
}
