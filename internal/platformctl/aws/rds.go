package aws

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type RDSInstance struct {
	Identifier            string
	ARN                   string
	ResourceID            string
	Status                string
	Engine                string
	EngineVersion         string
	InstanceClass         string
	AllocatedStorageGiB   int
	StorageType           string
	Port                  int
	Endpoint              string
	SubnetGroup           string
	VPCID                 string
	SecurityGroupIDs      []string
	BackupRetentionDays   int
	PubliclyAccessible    bool
	StorageEncrypted      bool
	MultiAZ               bool
	DeletionProtection    bool
	LatestRestorableTime  time.Time
	MasterUserSecretARN   string
	MasterUserSecretState string
}

type RDSRestoreWindow struct {
	Status       string
	Encrypted    bool
	Retention    int
	EarliestTime time.Time
	LatestTime   time.Time
}

type RDSPointInTimeRestoreRequest struct {
	SourceIdentifier    string
	TargetIdentifier    string
	RestoreTime         time.Time
	InstanceClass       string
	SubnetGroup         string
	SecurityGroupIDs    []string
	Port                int
	BackupRetentionDays int
	PubliclyAccessible  bool
	MultiAZ             bool
	DeletionProtection  bool
	Tags                map[string]string
}

type TaggedRDSResource struct {
	ARN  string
	Tags map[string]string
}

func (c Client) DescribeRDSInstance(ctx context.Context, identifier string) (RDSInstance, error) {
	if identifier == "" {
		return RDSInstance{}, errors.New("RDS instance identifier is required")
	}
	var response struct {
		Instances []struct {
			Identifier         string    `json:"DBInstanceIdentifier"`
			ARN                string    `json:"DBInstanceArn"`
			ResourceID         string    `json:"DbiResourceId"`
			Status             string    `json:"DBInstanceStatus"`
			Engine             string    `json:"Engine"`
			EngineVersion      string    `json:"EngineVersion"`
			InstanceClass      string    `json:"DBInstanceClass"`
			AllocatedStorage   int       `json:"AllocatedStorage"`
			StorageType        string    `json:"StorageType"`
			Port               int       `json:"Port"`
			BackupRetention    int       `json:"BackupRetentionPeriod"`
			PubliclyAccessible bool      `json:"PubliclyAccessible"`
			StorageEncrypted   bool      `json:"StorageEncrypted"`
			MultiAZ            bool      `json:"MultiAZ"`
			DeletionProtection bool      `json:"DeletionProtection"`
			LatestRestorable   time.Time `json:"LatestRestorableTime"`
			Endpoint           struct {
				Address string `json:"Address"`
				Port    int    `json:"Port"`
			} `json:"Endpoint"`
			SubnetGroup struct {
				Name  string `json:"DBSubnetGroupName"`
				VPCID string `json:"VpcId"`
			} `json:"DBSubnetGroup"`
			SecurityGroups []struct {
				ID string `json:"VpcSecurityGroupId"`
			} `json:"VpcSecurityGroups"`
			MasterSecret struct {
				ARN    string `json:"SecretArn"`
				Status string `json:"SecretStatus"`
			} `json:"MasterUserSecret"`
		} `json:"DBInstances"`
	}
	if err := c.JSON(ctx, &response, "rds", "describe-db-instances",
		"--db-instance-identifier", identifier, "--output", "json"); err != nil {
		return RDSInstance{}, fmt.Errorf("describe RDS instance %s: %w", identifier, err)
	}
	if len(response.Instances) != 1 {
		return RDSInstance{}, fmt.Errorf("describe RDS instance %s returned %d instances", identifier, len(response.Instances))
	}
	value := response.Instances[0]
	instance := RDSInstance{
		Identifier: value.Identifier, ARN: value.ARN, ResourceID: value.ResourceID,
		Status: value.Status, Engine: value.Engine, EngineVersion: value.EngineVersion,
		InstanceClass: value.InstanceClass, AllocatedStorageGiB: value.AllocatedStorage,
		StorageType: value.StorageType, Port: value.Port, Endpoint: value.Endpoint.Address,
		SubnetGroup: value.SubnetGroup.Name, VPCID: value.SubnetGroup.VPCID,
		BackupRetentionDays: value.BackupRetention, PubliclyAccessible: value.PubliclyAccessible,
		StorageEncrypted: value.StorageEncrypted, MultiAZ: value.MultiAZ,
		DeletionProtection: value.DeletionProtection, LatestRestorableTime: value.LatestRestorable,
		MasterUserSecretARN: value.MasterSecret.ARN, MasterUserSecretState: value.MasterSecret.Status,
	}
	if instance.Port == 0 {
		instance.Port = value.Endpoint.Port
	}
	for _, group := range value.SecurityGroups {
		instance.SecurityGroupIDs = append(instance.SecurityGroupIDs, group.ID)
	}
	sort.Strings(instance.SecurityGroupIDs)
	return instance, nil
}

func (c Client) DescribeRDSRestoreWindow(ctx context.Context, identifier string) (RDSRestoreWindow, error) {
	var response struct {
		Backups []struct {
			Status    string `json:"Status"`
			Encrypted bool   `json:"Encrypted"`
			Retention int    `json:"BackupRetentionPeriod"`
			Window    struct {
				Earliest time.Time `json:"EarliestTime"`
				Latest   time.Time `json:"LatestTime"`
			} `json:"RestoreWindow"`
		} `json:"DBInstanceAutomatedBackups"`
	}
	if err := c.JSON(ctx, &response, "rds", "describe-db-instance-automated-backups",
		"--db-instance-identifier", identifier, "--output", "json"); err != nil {
		return RDSRestoreWindow{}, fmt.Errorf("describe RDS restore window %s: %w", identifier, err)
	}
	if len(response.Backups) != 1 {
		return RDSRestoreWindow{}, fmt.Errorf("RDS restore window %s returned %d backups", identifier, len(response.Backups))
	}
	backup := response.Backups[0]
	return RDSRestoreWindow{
		Status: backup.Status, Encrypted: backup.Encrypted, Retention: backup.Retention,
		EarliestTime: backup.Window.Earliest, LatestTime: backup.Window.Latest,
	}, nil
}

func (c Client) RestoreRDSPointInTime(ctx context.Context, request RDSPointInTimeRestoreRequest) (RDSInstance, error) {
	if request.SourceIdentifier == "" || request.TargetIdentifier == "" || request.RestoreTime.IsZero() {
		return RDSInstance{}, errors.New("source, target and restore time are required")
	}
	if request.SourceIdentifier == request.TargetIdentifier {
		return RDSInstance{}, errors.New("RDS restore target must differ from source")
	}
	if request.SubnetGroup == "" || len(request.SecurityGroupIDs) == 0 || request.InstanceClass == "" {
		return RDSInstance{}, errors.New("RDS restore class, subnet group and security groups are required")
	}
	args := []string{
		"rds", "restore-db-instance-to-point-in-time",
		"--source-db-instance-identifier", request.SourceIdentifier,
		"--target-db-instance-identifier", request.TargetIdentifier,
		"--restore-time", request.RestoreTime.UTC().Format(time.RFC3339Nano),
		"--db-instance-class", request.InstanceClass,
		"--db-subnet-group-name", request.SubnetGroup,
		"--vpc-security-group-ids",
	}
	args = append(args, request.SecurityGroupIDs...)
	args = append(args,
		"--port", fmt.Sprint(request.Port),
		"--backup-retention-period", fmt.Sprint(request.BackupRetentionDays),
	)
	if request.PubliclyAccessible {
		args = append(args, "--publicly-accessible")
	} else {
		args = append(args, "--no-publicly-accessible")
	}
	if request.MultiAZ {
		args = append(args, "--multi-az")
	} else {
		args = append(args, "--no-multi-az")
	}
	if request.DeletionProtection {
		args = append(args, "--deletion-protection")
	} else {
		args = append(args, "--no-deletion-protection")
	}
	if len(request.Tags) > 0 {
		args = append(args, "--tags")
		keys := make([]string, 0, len(request.Tags))
		for key := range request.Tags {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if strings.ContainsAny(key+request.Tags[key], ",\n\r") {
				return RDSInstance{}, fmt.Errorf("RDS tag %s contains unsupported characters", key)
			}
			args = append(args, "Key="+key+",Value="+request.Tags[key])
		}
	}
	args = append(args, "--output", "json")
	var response struct {
		Instance struct {
			Identifier string `json:"DBInstanceIdentifier"`
			ARN        string `json:"DBInstanceArn"`
			ResourceID string `json:"DbiResourceId"`
			Status     string `json:"DBInstanceStatus"`
			Endpoint   struct {
				Address string `json:"Address"`
				Port    int    `json:"Port"`
			} `json:"Endpoint"`
		} `json:"DBInstance"`
	}
	if err := c.JSON(ctx, &response, args...); err != nil {
		return RDSInstance{}, fmt.Errorf("restore RDS target %s: %w", request.TargetIdentifier, err)
	}
	return RDSInstance{
		Identifier: response.Instance.Identifier, ARN: response.Instance.ARN,
		ResourceID: response.Instance.ResourceID, Status: response.Instance.Status,
		Endpoint: response.Instance.Endpoint.Address, Port: response.Instance.Endpoint.Port,
	}, nil
}

func (c Client) DeleteRDSInstance(ctx context.Context, identifier string) error {
	if identifier == "" {
		return errors.New("RDS delete identifier is required")
	}
	if err := c.Run(ctx, "rds", "delete-db-instance", "--db-instance-identifier", identifier,
		"--skip-final-snapshot", "--delete-automated-backups"); err != nil {
		return fmt.Errorf("delete RDS instance %s: %w", identifier, err)
	}
	return nil
}

func (c Client) RDSTags(ctx context.Context, arn string) (map[string]string, error) {
	if arn == "" {
		return nil, errors.New("RDS ARN is required")
	}
	var response struct {
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"TagList"`
	}
	if err := c.JSON(ctx, &response, "rds", "list-tags-for-resource", "--resource-name", arn, "--output", "json"); err != nil {
		return nil, fmt.Errorf("list RDS tags for %s: %w", arn, err)
	}
	result := make(map[string]string, len(response.Tags))
	for _, tag := range response.Tags {
		result[tag.Key] = tag.Value
	}
	return result, nil
}

func (c Client) FindTaggedRDSResources(ctx context.Context, tags map[string]string) ([]TaggedRDSResource, error) {
	args := []string{"resourcegroupstaggingapi", "get-resources", "--resource-type-filters", "rds:db"}
	if len(tags) > 0 {
		args = append(args, "--tag-filters")
		keys := make([]string, 0, len(tags))
		for key := range tags {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if strings.ContainsAny(key+tags[key], ",\n\r") {
				return nil, fmt.Errorf("RDS tag filter %s contains unsupported characters", key)
			}
			args = append(args, "Key="+key+",Values="+tags[key])
		}
	}
	args = append(args, "--output", "json")
	var response struct {
		Resources []struct {
			ARN  string `json:"ResourceARN"`
			Tags []struct {
				Key   string `json:"Key"`
				Value string `json:"Value"`
			} `json:"Tags"`
		} `json:"ResourceTagMappingList"`
	}
	if err := c.JSON(ctx, &response, args...); err != nil {
		return nil, fmt.Errorf("find tagged RDS resources: %w", err)
	}
	result := make([]TaggedRDSResource, 0, len(response.Resources))
	for _, resource := range response.Resources {
		value := TaggedRDSResource{ARN: resource.ARN, Tags: map[string]string{}}
		for _, tag := range resource.Tags {
			value.Tags[tag.Key] = tag.Value
		}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ARN < result[j].ARN })
	return result, nil
}
