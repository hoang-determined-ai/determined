package provisioner

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/pkg/errors"

	"github.com/determined-ai/determined/master/pkg"
	"github.com/determined-ai/determined/master/pkg/check"
	"github.com/determined-ai/determined/master/pkg/device"
)

const spotPriceNotSetPlaceholder = "OnDemand"

// AWSClusterConfig describes the configuration for an EC2 cluster managed by Determined.
type AWSClusterConfig struct {
	Region string `json:"region"`

	RootVolumeSize int    `json:"root_volume_size"`
	ImageID        string `json:"image_id"`

	TagKey       string `json:"tag_key"`
	TagValue     string `json:"tag_value"`
	InstanceName string `json:"instance_name"`

	SSHKeyName            string              `json:"ssh_key_name"`
	NetworkInterface      ec2NetworkInterface `json:"network_interface"`
	IamInstanceProfileArn string              `json:"iam_instance_profile_arn"`

	InstanceType  ec2InstanceType `json:"instance_type"`
	InstanceSlots *int            `json:"instance_slots,omitempty"`

	LogGroup  string `json:"log_group"`
	LogStream string `json:"log_stream"`

	SpotEnabled  bool   `json:"spot"`
	SpotMaxPrice string `json:"spot_max_price"`

	CustomTags []*ec2Tag `json:"custom_tags"`

	CPUSlotsAllowed bool `json:"cpu_slots_allowed"`
}

var defaultAWSImageID = map[string]string{
	"ap-northeast-1": "ami-07be463fe74180d9c",
	"ap-northeast-2": "ami-0bc9dc1a714ff6d82",
	"ap-southeast-1": "ami-0e95df3b2b20d245c",
	"ap-southeast-2": "ami-054bc72b30aac5c40",
	"us-east-2":      "ami-0eb5a8cd6ebc8f40f",
	"us-east-1":      "ami-0fb9574129921911f",
	"us-west-2":      "ami-02d50e97a6fff984c",
	"eu-central-1":   "ami-04f8cd92c6e00a1f8",
	"eu-west-2":      "ami-0b159faec2954f54a",
	"eu-west-1":      "ami-037c51c9b79d49a02",
}

var defaultAWSClusterConfig = AWSClusterConfig{
	InstanceName:   "determined-ai-agent",
	RootVolumeSize: 200,
	TagKey:         "managed_by",
	NetworkInterface: ec2NetworkInterface{
		PublicIP: true,
	},
	InstanceType:    "p3.8xlarge",
	SpotEnabled:     false,
	CPUSlotsAllowed: false,
}

func (c *AWSClusterConfig) buildDockerLogString() string {
	logString := ""
	if c.LogGroup != "" {
		logString += "--log-driver=awslogs --log-opt awslogs-group=" + c.LogGroup
	}
	if c.LogStream != "" {
		logString += " --log-opt awslogs-stream=" + c.LogStream
	}
	return logString
}

func (c *AWSClusterConfig) initDefaultValues() error {
	metadata, err := getEC2MetadataSess()
	if err != nil {
		return err
	}

	if len(c.Region) == 0 {
		if c.Region, err = metadata.Region(); err != nil {
			return err
		}
	}

	if len(c.SpotMaxPrice) == 0 {
		c.SpotMaxPrice = spotPriceNotSetPlaceholder
	}

	if len(c.ImageID) == 0 {
		if v, ok := defaultAWSImageID[c.Region]; ok {
			c.ImageID = v
		} else {
			return errors.Errorf("cannot find default image ID in the region %s", c.Region)
		}
	}

	// One common reason that metadata.GetInstanceIdentityDocument() fails is that the master is not
	// running in EC2. Use a default name here rather than holding up initializing the provider.
	identifier := pkg.DeterminedIdentifier
	idDoc, err := metadata.GetInstanceIdentityDocument()
	if err == nil {
		identifier = idDoc.InstanceID
	}

	if len(c.TagValue) == 0 {
		c.TagValue = identifier
	}
	return nil
}

// UnmarshalJSON implements the json.Unmarshaler interface.
func (c *AWSClusterConfig) UnmarshalJSON(data []byte) error {
	*c = defaultAWSClusterConfig
	type DefaultParser *AWSClusterConfig
	return json.Unmarshal(data, DefaultParser(c))
}

func validateInstanceTypeSlots(c AWSClusterConfig) error {
	// Must have an instance in ec2InstanceSlots map or InstanceSlots set
	instanceType := c.InstanceType
	if _, ok := ec2InstanceSlots[instanceType]; ok {
		return nil
	}

	instanceSlots := c.InstanceSlots
	if instanceSlots != nil {
		if *instanceSlots < 0 {
			return errors.Errorf("ec2 'instance_slots' must be greater than or equal to 0")
		}
		ec2InstanceSlots[instanceType] = *instanceSlots
		return nil
	}

	strs := make([]string, 0, len(ec2InstanceSlots))
	for t := range ec2InstanceSlots {
		strs = append(strs, t.name())
	}
	return errors.Errorf("Either ec2 'instance_type' and 'instance_slots' must be specified or "+
		"the ec2 'instance_type' must be one of types: %s", strings.Join(strs, ", "))
}

// Validate implements the check.Validatable interface.
func (c AWSClusterConfig) Validate() []error {
	var spotPriceIsNotValidNumberErr error
	if c.SpotEnabled && c.SpotMaxPrice != spotPriceNotSetPlaceholder {
		spotPriceIsNotValidNumberErr = validateMaxSpotPrice(c.SpotMaxPrice)
	}
	return []error{
		check.GreaterThan(len(c.SSHKeyName), 0, "ec2 key name must be non-empty"),
		check.GreaterThanOrEqualTo(c.RootVolumeSize, 100, "ec2 root volume size must be >= 100"),
		spotPriceIsNotValidNumberErr,
		validateInstanceTypeSlots(c),
	}
}

// SlotsPerInstance returns the number of slots per instance.
func (c AWSClusterConfig) SlotsPerInstance() int {
	slots := c.InstanceType.Slots()
	if slots == 0 && c.CPUSlotsAllowed {
		slots = 1
	}

	return slots
}

// SlotType returns the type of the slot.
func (c AWSClusterConfig) SlotType() device.Type {
	slots := c.InstanceType.Slots()
	if slots > 0 {
		return device.CUDA
	}
	if c.CPUSlotsAllowed {
		return device.CPU
	}
	return device.ZeroSlot
}

func validateMaxSpotPrice(spotMaxPriceInput string) error {
	// Must have 1 or 0 decimalPoints. All other characters must be digits
	numDecimalPoints := strings.Count(spotMaxPriceInput, ".")
	if numDecimalPoints != 0 && numDecimalPoints != 1 {
		return errors.New(
			fmt.Sprintf("spot max price should have either 0 or 1 decimal points. "+
				"Received %s, which has %d decimal points",
				spotMaxPriceInput,
				numDecimalPoints))
	}

	priceWithoutDecimalPoint := strings.Replace(spotMaxPriceInput, ".", "", -1)
	for _, char := range priceWithoutDecimalPoint {
		if !unicode.IsDigit(char) {
			return errors.New(
				fmt.Sprintf("spot max price should only contain digits and, optionally, one decimal point. "+
					"Received %s, which has the non-digit character %s",
					spotMaxPriceInput,
					string(char)))
		}
	}
	return nil
}

type ec2NetworkInterface struct {
	PublicIP        bool   `json:"public_ip"`
	SubnetID        string `json:"subnet_id"`
	SecurityGroupID string `json:"security_group_id"`
}

type ec2Tag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ec2InstanceType string

func (t ec2InstanceType) name() string {
	return string(t)
}

func (t ec2InstanceType) Slots() int {
	if s, ok := ec2InstanceSlots[t]; ok {
		return s
	}
	return 0
}

// This map tracks how many slots are available in each instance type. It also
// serves as the list of instance types that the provisioner may provision - if
// the master.yaml is configured with an instance type and instance slots are
// not specified the provisioner will consider it an error.
var ec2InstanceSlots = map[ec2InstanceType]int{
	"g4dn.xlarge":   1,
	"g4dn.2xlarge":  1,
	"g4dn.4xlarge":  1,
	"g4dn.8xlarge":  1,
	"g4dn.16xlarge": 1,
	"g4dn.12xlarge": 4,
	"g4dn.metal":    8,
	"p2.xlarge":     1,
	"p2.8xlarge":    8,
	"p2.16xlarge":   16,
	"p3.2xlarge":    1,
	"p3.8xlarge":    4,
	"p3.16xlarge":   8,
	"p3dn.24xlarge": 8,
	"t2.medium":     0,
	"t2.large":      0,
	"t2.xlarge":     0,
	"t2.2xlarge":    0,
	"t3.nano":       0,
	"t3.micro":      0,
	"t3.small":      0,
	"t3.medium":     0,
	"t3.large":      0,
	"t3.xlarge":     0,
	"t3.2xlarge":    0,
	"c4.large":      0,
	"c4.xlarge":     0,
	"c4.2xlarge":    0,
	"c4.4xlarge":    0,
	"c4.8xlarge":    0,
	"c5.large":      0,
	"c5.xlarge":     0,
	"c5.2xlarge":    0,
	"c5.4xlarge":    0,
	"c5.9xlarge":    0,
	"c5.12xlarge":   0,
	"c5.18xlarge":   0,
	"c5.24xlarge":   0,
	"c5d.large":     0,
	"c5d.xlarge":    0,
	"c5d.2xlarge":   0,
	"c5d.4xlarge":   0,
	"c5d.9xlarge":   0,
	"c5d.12xlarge":  0,
	"c5d.18xlarge":  0,
	"c5d.24xlarge":  0,
	"c5n.large":     0,
	"c5n.xlarge":    0,
	"c5n.2xlarge":   0,
	"c5n.4xlarge":   0,
	"c5n.9xlarge":   0,
	"c5n.18xlarge":  0,
	"m4.large":      0,
	"m4.xlarge":     0,
	"m4.2xlarge":    0,
	"m4.4xlarge":    0,
	"m4.10xlarge":   0,
	"m4.16xlarge":   0,
	"m5.large":      0,
	"m5.xlarge":     0,
	"m5.2xlarge":    0,
	"m5.4xlarge":    0,
	"m5.8xlarge":    0,
	"m5.12xlarge":   0,
	"m5.16xlarge":   0,
	"m5.24xlarge":   0,
	"m5d.large":     0,
	"m5d.xlarge":    0,
	"m5d.2xlarge":   0,
	"m5d.4xlarge":   0,
	"m5d.8xlarge":   0,
	"m5d.12xlarge":  0,
	"m5d.16xlarge":  0,
	"m5d.24xlarge":  0,
	"m5dn.large":    0,
	"m5dn.xlarge":   0,
	"m5dn.2xlarge":  0,
	"m5dn.4xlarge":  0,
	"m5dn.8xlarge":  0,
	"m5dn.12xlarge": 0,
	"m5dn.16xlarge": 0,
	"m5dn.24xlarge": 0,
	"m5n.large":     0,
	"m5n.xlarge":    0,
	"m5n.2xlarge":   0,
	"m5n.4xlarge":   0,
	"m5n.8xlarge":   0,
	"m5n.12xlarge":  0,
	"m5n.16xlarge":  0,
	"m5n.24xlarge":  0,
	"m5zn.large":    0,
	"m5zn.xlarge":   0,
	"m5zn.2xlarge":  0,
	"m5zn.3xlarge":  0,
	"m5zn.6xlarge":  0,
	"m5zn.12xlarge": 0,
}
