package internal

import (
	"context"
	"fmt"

	"github.com/GoCodeAlone/workflow/interfaces"
	pb "github.com/GoCodeAlone/workflow/plugin/external/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *ncIaCServer) resolveResourceDriver(resourceType string) (interfaces.ResourceDriver, error) {
	if resourceType == "" {
		return nil, status.Error(codes.InvalidArgument, "namecheap ResourceDriver: resource_type is required")
	}
	if s.dnsDriver == nil || s.delegationDriver == nil || s.transferDriver == nil {
		return nil, status.Error(codes.FailedPrecondition, "namecheap ResourceDriver: Initialize must be called before resource driver RPCs")
	}
	driver, err := s.resourceDriver(resourceType)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "namecheap ResourceDriver: %v", err)
	}
	return driver, nil
}

func (s *ncIaCServer) Create(ctx context.Context, req *pb.ResourceCreateRequest) (*pb.ResourceCreateResponse, error) {
	driver, err := s.resolveResourceDriver(req.GetResourceType())
	if err != nil {
		return nil, err
	}
	spec, err := specFromPBNC(req.GetSpec())
	if err != nil {
		return nil, fmt.Errorf("namecheap ResourceDriver(%s).Create: decode spec: %w", req.GetResourceType(), err)
	}
	out, err := driver.Create(ctx, spec)
	if err != nil {
		return nil, err
	}
	pbOut, err := outputToPBNC(out)
	if err != nil {
		return nil, fmt.Errorf("namecheap ResourceDriver(%s).Create: encode response: %w", req.GetResourceType(), err)
	}
	return &pb.ResourceCreateResponse{Output: pbOut}, nil
}

func (s *ncIaCServer) Read(ctx context.Context, req *pb.ResourceReadRequest) (*pb.ResourceReadResponse, error) {
	driver, err := s.resolveResourceDriver(req.GetResourceType())
	if err != nil {
		return nil, err
	}
	out, err := driver.Read(ctx, refFromPBNC(req.GetRef()))
	if err != nil {
		return nil, err
	}
	pbOut, err := outputToPBNC(out)
	if err != nil {
		return nil, fmt.Errorf("namecheap ResourceDriver(%s).Read: encode response: %w", req.GetResourceType(), err)
	}
	return &pb.ResourceReadResponse{Output: pbOut}, nil
}

func (s *ncIaCServer) Update(ctx context.Context, req *pb.ResourceUpdateRequest) (*pb.ResourceUpdateResponse, error) {
	driver, err := s.resolveResourceDriver(req.GetResourceType())
	if err != nil {
		return nil, err
	}
	spec, err := specFromPBNC(req.GetSpec())
	if err != nil {
		return nil, fmt.Errorf("namecheap ResourceDriver(%s).Update: decode spec: %w", req.GetResourceType(), err)
	}
	out, err := driver.Update(ctx, refFromPBNC(req.GetRef()), spec)
	if err != nil {
		return nil, err
	}
	pbOut, err := outputToPBNC(out)
	if err != nil {
		return nil, fmt.Errorf("namecheap ResourceDriver(%s).Update: encode response: %w", req.GetResourceType(), err)
	}
	return &pb.ResourceUpdateResponse{Output: pbOut}, nil
}

func (s *ncIaCServer) Delete(ctx context.Context, req *pb.ResourceDeleteRequest) (*pb.ResourceDeleteResponse, error) {
	driver, err := s.resolveResourceDriver(req.GetResourceType())
	if err != nil {
		return nil, err
	}
	if err := driver.Delete(ctx, refFromPBNC(req.GetRef())); err != nil {
		return nil, err
	}
	return &pb.ResourceDeleteResponse{}, nil
}

func (s *ncIaCServer) Diff(ctx context.Context, req *pb.ResourceDiffRequest) (*pb.ResourceDiffResponse, error) {
	driver, err := s.resolveResourceDriver(req.GetResourceType())
	if err != nil {
		return nil, err
	}
	desired, err := specFromPBNC(req.GetDesired())
	if err != nil {
		return nil, fmt.Errorf("namecheap ResourceDriver(%s).Diff: decode desired: %w", req.GetResourceType(), err)
	}
	current, err := outputFromPBNC(req.GetCurrent())
	if err != nil {
		return nil, fmt.Errorf("namecheap ResourceDriver(%s).Diff: decode current: %w", req.GetResourceType(), err)
	}
	result, err := driver.Diff(ctx, desired, current)
	if err != nil {
		return nil, err
	}
	pbResult, err := diffResultToPBNC(result)
	if err != nil {
		return nil, fmt.Errorf("namecheap ResourceDriver(%s).Diff: encode response: %w", req.GetResourceType(), err)
	}
	return &pb.ResourceDiffResponse{Result: pbResult}, nil
}

func (s *ncIaCServer) Scale(ctx context.Context, req *pb.ResourceScaleRequest) (*pb.ResourceScaleResponse, error) {
	driver, err := s.resolveResourceDriver(req.GetResourceType())
	if err != nil {
		return nil, err
	}
	out, err := driver.Scale(ctx, refFromPBNC(req.GetRef()), int(req.GetReplicas()))
	if err != nil {
		return nil, err
	}
	pbOut, err := outputToPBNC(out)
	if err != nil {
		return nil, fmt.Errorf("namecheap ResourceDriver(%s).Scale: encode response: %w", req.GetResourceType(), err)
	}
	return &pb.ResourceScaleResponse{Output: pbOut}, nil
}

func (s *ncIaCServer) HealthCheck(ctx context.Context, req *pb.ResourceHealthCheckRequest) (*pb.ResourceHealthCheckResponse, error) {
	driver, err := s.resolveResourceDriver(req.GetResourceType())
	if err != nil {
		return nil, err
	}
	result, err := driver.HealthCheck(ctx, refFromPBNC(req.GetRef()))
	if err != nil {
		return nil, err
	}
	return &pb.ResourceHealthCheckResponse{Result: healthResultToPBNC(result)}, nil
}

func (s *ncIaCServer) SensitiveKeys(_ context.Context, req *pb.SensitiveKeysRequest) (*pb.SensitiveKeysResponse, error) {
	driver, err := s.resolveResourceDriver(req.GetResourceType())
	if err != nil {
		return nil, err
	}
	return &pb.SensitiveKeysResponse{Keys: append([]string(nil), driver.SensitiveKeys()...)}, nil
}

func (s *ncIaCServer) Troubleshoot(ctx context.Context, req *pb.TroubleshootRequest) (*pb.TroubleshootResponse, error) {
	driver, err := s.resolveResourceDriver(req.GetResourceType())
	if err != nil {
		return nil, err
	}
	tr, ok := driver.(interfaces.Troubleshooter)
	if !ok {
		return nil, status.Errorf(codes.Unimplemented,
			"namecheap ResourceDriver(%s).Troubleshoot: driver does not implement interfaces.Troubleshooter",
			req.GetResourceType())
	}
	diags, err := tr.Troubleshoot(ctx, refFromPBNC(req.GetRef()), req.GetFailureMsg())
	if err != nil {
		return nil, err
	}
	out := make([]*pb.Diagnostic, 0, len(diags))
	for _, d := range diags {
		out = append(out, &pb.Diagnostic{
			Id:     d.ID,
			Phase:  d.Phase,
			Cause:  d.Cause,
			At:     timeToPBNC(d.At),
			Detail: d.Detail,
		})
	}
	return &pb.TroubleshootResponse{Diagnostics: out}, nil
}

func outputToPBNC(out *interfaces.ResourceOutput) (*pb.ResourceOutput, error) {
	if out == nil {
		return nil, nil
	}
	outputsJSON, err := marshalJSONMapNC(out.Outputs)
	if err != nil {
		return nil, err
	}
	return &pb.ResourceOutput{
		Name:        out.Name,
		Type:        out.Type,
		ProviderId:  out.ProviderID,
		OutputsJson: outputsJSON,
		Sensitive:   copyBoolMapNC(out.Sensitive),
		Status:      out.Status,
	}, nil
}

func outputFromPBNC(out *pb.ResourceOutput) (*interfaces.ResourceOutput, error) {
	if out == nil {
		return nil, nil
	}
	outputs, err := unmarshalJSONMapNC(out.GetOutputsJson())
	if err != nil {
		return nil, err
	}
	return &interfaces.ResourceOutput{
		Name:       out.GetName(),
		Type:       out.GetType(),
		ProviderID: out.GetProviderId(),
		Outputs:    outputs,
		Sensitive:  copyBoolMapNC(out.GetSensitive()),
		Status:     out.GetStatus(),
	}, nil
}

func diffResultToPBNC(result *interfaces.DiffResult) (*pb.DiffResult, error) {
	if result == nil {
		return nil, nil
	}
	changes, err := changesToPBNC(result.Changes)
	if err != nil {
		return nil, err
	}
	return &pb.DiffResult{
		NeedsUpdate:  result.NeedsUpdate,
		NeedsReplace: result.NeedsReplace,
		Changes:      changes,
	}, nil
}

func healthResultToPBNC(result *interfaces.HealthResult) *pb.HealthResult {
	if result == nil {
		return nil
	}
	return &pb.HealthResult{Healthy: result.Healthy, Message: result.Message}
}

func copyBoolMapNC(in map[string]bool) map[string]bool {
	if in == nil {
		return nil
	}
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
