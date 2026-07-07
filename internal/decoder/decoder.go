// Package decoder converts DynamoDB Stream event images into typed desire status structs.
// The aws-lambda-go SDK delivers stream records with their own AttributeValue type
// (events.DynamoDBAttributeValue), so we decode manually rather than using
// the attributevalue.UnmarshalMap helper (which expects dynamodb/types.AttributeValue).
package decoder

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

// DesireType identifies which of the 3 status tables a record came from.
type DesireType string

const (
	DesireTypeApply  DesireType = "apply"
	DesireTypeDelete DesireType = "delete"
	DesireTypeRead   DesireType = "read"
)

// Condition mirrors metav1.Condition for the subset we persist.
type Condition struct {
	Type               string    `json:"type"`
	Status             string    `json:"status"`
	Reason             string    `json:"reason"`
	Message            string    `json:"message"`
	LastTransitionTime time.Time `json:"lastTransitionTime"`
}

// ResourceReference identifies a Kubernetes resource.
type ResourceReference struct {
	Group     string `json:"group"`
	Version   string `json:"version"`
	Resource  string `json:"resource"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// BaseStatus holds fields common to all three desire status types.
type BaseStatus struct {
	DocumentID               string
	ManagementCluster        string
	ClusterID                string
	NodePoolName             string
	Version                  int64
	UpdateTime               time.Time
	CreateTime               *time.Time
	TargetItem               ResourceReference
	Conditions               []Condition
	ObservedDesireUpdateTime *time.Time
	StreamARN                string
}

// ApplyDesireStatus is the decoded status for an apply desire.
type ApplyDesireStatus struct {
	BaseStatus
	AppliedResourceGeneration int64
}

// DeleteDesireStatus is the decoded status for a delete desire.
type DeleteDesireStatus struct {
	BaseStatus
}

// ReadDesireStatus is the decoded status for a read desire.
type ReadDesireStatus struct {
	BaseStatus
	// KubeContent holds the raw JSON of the mirrored Kubernetes object.
	// nil means the object does not exist on the management cluster.
	KubeContent json.RawMessage
}

// DesireTypeFromStreamARN infers the desire type from the stream ARN suffix.
// Stream ARNs contain the table name, e.g.:
//
//	arn:aws:dynamodb:us-east-1:123:table/mc01-status-applydesires/stream/...
func DesireTypeFromStreamARN(arn string) (DesireType, error) {
	// Walk backwards through the ARN to find the table name segment.
	for i := len(arn) - 1; i >= 0; i-- {
		if arn[i] == '/' {
			segment := arn[i+1:]
			// Table name is before the next slash (stream/timestamp).
			for j := 0; j < len(segment); j++ {
				if segment[j] == '/' {
					segment = segment[:j]
					break
				}
			}
			switch {
			case len(segment) > len("applydesires") && segment[len(segment)-len("applydesires"):] == "applydesires":
				return DesireTypeApply, nil
			case len(segment) > len("deletedesires") && segment[len(segment)-len("deletedesires"):] == "deletedesires":
				return DesireTypeDelete, nil
			case len(segment) > len("readdesires") && segment[len(segment)-len("readdesires"):] == "readdesires":
				return DesireTypeRead, nil
			}
		}
	}
	return "", fmt.Errorf("cannot determine desire type from stream ARN: %s", arn)
}

// DecodeApply decodes a DynamoDB stream image into an ApplyDesireStatus.
func DecodeApply(image map[string]events.DynamoDBAttributeValue, streamARN string) (*ApplyDesireStatus, error) {
	base, err := decodeBase(image, streamARN)
	if err != nil {
		return nil, err
	}

	result := &ApplyDesireStatus{BaseStatus: *base}

	if v, ok := image["status"]; ok && v.DataType() == events.DataTypeMap {
		statusMap := v.Map()
		if gen, ok := statusMap["appliedResourceGeneration"]; ok && gen.DataType() == events.DataTypeNumber {
			result.AppliedResourceGeneration, err = parseInt64(gen.Number())
			if err != nil {
				return nil, fmt.Errorf("appliedResourceGeneration: %w", err)
			}
		}
	}

	return result, nil
}

// DecodeDelete decodes a DynamoDB stream image into a DeleteDesireStatus.
func DecodeDelete(image map[string]events.DynamoDBAttributeValue, streamARN string) (*DeleteDesireStatus, error) {
	base, err := decodeBase(image, streamARN)
	if err != nil {
		return nil, err
	}
	return &DeleteDesireStatus{BaseStatus: *base}, nil
}

// DecodeRead decodes a DynamoDB stream image into a ReadDesireStatus.
func DecodeRead(image map[string]events.DynamoDBAttributeValue, streamARN string) (*ReadDesireStatus, error) {
	base, err := decodeBase(image, streamARN)
	if err != nil {
		return nil, err
	}

	result := &ReadDesireStatus{BaseStatus: *base}

	// status_kubeContent is a top-level S attribute (not nested inside "status").
	// See kube-applier-aws internal/database/rawext_codec.go.
	if v, ok := image["status_kubeContent"]; ok && v.DataType() == events.DataTypeString {
		s := v.String()
		if s != "" && json.Valid([]byte(s)) {
			result.KubeContent = json.RawMessage(s)
		}
	}

	return result, nil
}

// decodeBase extracts the fields common to all three desire types.
func decodeBase(image map[string]events.DynamoDBAttributeValue, streamARN string) (*BaseStatus, error) {
	base := &BaseStatus{StreamARN: streamARN}
	var err error

	if v, ok := image["documentID"]; ok {
		base.DocumentID = v.String()
	}
	if base.DocumentID == "" {
		return nil, fmt.Errorf("missing documentID in stream image")
	}

	if v, ok := image["version"]; ok && v.DataType() == events.DataTypeNumber {
		base.Version, err = parseInt64(v.Number())
		if err != nil {
			return nil, fmt.Errorf("version: %w", err)
		}
	}

	if v, ok := image["updateTime"]; ok && v.DataType() == events.DataTypeString {
		base.UpdateTime, err = time.Parse(time.RFC3339Nano, v.String())
		if err != nil {
			return nil, fmt.Errorf("updateTime: %w", err)
		}
	}

	if v, ok := image["createTime"]; ok && v.DataType() == events.DataTypeString && v.String() != "" {
		t, err := time.Parse(time.RFC3339Nano, v.String())
		if err != nil {
			return nil, fmt.Errorf("createTime: %w", err)
		}
		base.CreateTime = &t
	}

	if v, ok := image["spec"]; ok && v.DataType() == events.DataTypeMap {
		specMap := v.Map()
		base.ManagementCluster = stringVal(specMap, "managementCluster")
		base.ClusterID = stringVal(specMap, "clusterID")
		base.NodePoolName = stringVal(specMap, "nodePoolName")

		if ti, ok := specMap["targetItem"]; ok && ti.DataType() == events.DataTypeMap {
			base.TargetItem = decodeResourceReference(ti.Map())
		}
	}

	if v, ok := image["status"]; ok && v.DataType() == events.DataTypeMap {
		statusMap := v.Map()

		if conds, ok := statusMap["conditions"]; ok && conds.DataType() == events.DataTypeList {
			base.Conditions, err = decodeConditions(conds.List())
			if err != nil {
				return nil, fmt.Errorf("conditions: %w", err)
			}
		}

		if odt, ok := statusMap["observedDesireUpdateTime"]; ok && odt.DataType() == events.DataTypeString && odt.String() != "" {
			t, err := time.Parse(time.RFC3339Nano, odt.String())
			if err != nil {
				return nil, fmt.Errorf("observedDesireUpdateTime: %w", err)
			}
			base.ObservedDesireUpdateTime = &t
		}
	}

	return base, nil
}

func decodeResourceReference(m map[string]events.DynamoDBAttributeValue) ResourceReference {
	return ResourceReference{
		Group:     stringVal(m, "group"),
		Version:   stringVal(m, "version"),
		Resource:  stringVal(m, "resource"),
		Namespace: stringVal(m, "namespace"),
		Name:      stringVal(m, "name"),
	}
}

func decodeConditions(list []events.DynamoDBAttributeValue) ([]Condition, error) {
	conditions := make([]Condition, 0, len(list))
	for _, item := range list {
		if item.DataType() != events.DataTypeMap {
			continue
		}
		m := item.Map()
		c := Condition{
			Type:    stringVal(m, "type"),
			Status:  stringVal(m, "status"),
			Reason:  stringVal(m, "reason"),
			Message: stringVal(m, "message"),
		}
		if ltt, ok := m["lastTransitionTime"]; ok && ltt.DataType() == events.DataTypeString && ltt.String() != "" {
			t, err := time.Parse(time.RFC3339Nano, ltt.String())
			if err != nil {
				return nil, fmt.Errorf("lastTransitionTime: %w", err)
			}
			c.LastTransitionTime = t
		}
		conditions = append(conditions, c)
	}
	return conditions, nil
}

func stringVal(m map[string]events.DynamoDBAttributeValue, key string) string {
	if v, ok := m[key]; ok && v.DataType() == events.DataTypeString {
		return v.String()
	}
	return ""
}

func parseInt64(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}
