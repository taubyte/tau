package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/taubyte/tau/pkg/spore-drive/config/fixtures"
	pb "github.com/taubyte/tau/pkg/spore-drive/proto/gen/config/v1"
)

func TestDoShapes_List(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Shapes{Op: &pb.Shapes_List{List: true}}
	resp, err := service.doShapes(in, parser)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"shape1", "shape2"}, resp.Msg.GetSlice().GetValue())
}

func TestDoShapes_GetServices(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Shapes{
		Op: &pb.Shapes_Select{
			Select: &pb.Shape{
				Name: "shape1",
				Op: &pb.Shape_Services{
					Services: &pb.StringSliceOp{
						Op: &pb.StringSliceOp_List{List: true},
					},
				},
			},
		},
	}

	resp, err := service.doShapes(in, parser)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"auth", "seer"}, resp.Msg.GetSlice().GetValue())
}

func TestDoShapes_SetServices(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Shapes{
		Op: &pb.Shapes_Select{
			Select: &pb.Shape{
				Name: "shape1",
				Op: &pb.Shape_Services{
					Services: &pb.StringSliceOp{
						Op: &pb.StringSliceOp_Set{
							Set: &pb.StringSlice{Value: []string{"service1", "service2"}},
						},
					},
				},
			},
		},
	}

	_, err := service.doShapes(in, parser)
	assert.NoError(t, err)

	// Verify the change
	in = &pb.Shapes{
		Op: &pb.Shapes_Select{
			Select: &pb.Shape{
				Name: "shape1",
				Op: &pb.Shape_Services{
					Services: &pb.StringSliceOp{
						Op: &pb.StringSliceOp_List{List: true},
					},
				},
			},
		},
	}

	resp, err := service.doShapes(in, parser)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"service1", "service2"}, resp.Msg.GetSlice().GetValue())
}

func TestDoShapes_GetPorts(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Shapes{
		Op: &pb.Shapes_Select{
			Select: &pb.Shape{
				Name: "shape1",
				Op: &pb.Shape_Ports{
					Ports: &pb.Ports{
						Op: &pb.Ports_List{List: true},
					},
				},
			},
		},
	}

	resp, err := service.doShapes(in, parser)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"main", "lite"}, resp.Msg.GetSlice().GetValue())
}

func TestDoShapes_GetPortValue(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Get port value for "main"
	in := &pb.Shapes{
		Op: &pb.Shapes_Select{
			Select: &pb.Shape{
				Name: "shape1",
				Op: &pb.Shape_Ports{
					Ports: &pb.Ports{
						Op: &pb.Ports_Select{
							Select: &pb.Port{
								Name: "main",
								Op:   &pb.Port_Get{Get: true},
							},
						},
					},
				},
			},
		},
	}

	resp, err := service.doShapes(in, parser)
	assert.NoError(t, err)
	assert.Equal(t, uint64(4242), resp.Msg.GetUint64())
}

func TestDoShapes_SetPortValue(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Set port value for "main"
	in := &pb.Shapes{
		Op: &pb.Shapes_Select{
			Select: &pb.Shape{
				Name: "shape1",
				Op: &pb.Shape_Ports{
					Ports: &pb.Ports{
						Op: &pb.Ports_Select{
							Select: &pb.Port{
								Name: "main",
								Op:   &pb.Port_Set{Set: 8080},
							},
						},
					},
				},
			},
		},
	}

	_, err := service.doShapes(in, parser)
	assert.NoError(t, err)

	// Verify the change
	in = &pb.Shapes{
		Op: &pb.Shapes_Select{
			Select: &pb.Shape{
				Name: "shape1",
				Op: &pb.Shape_Ports{
					Ports: &pb.Ports{
						Op: &pb.Ports_Select{
							Select: &pb.Port{
								Name: "main",
								Op:   &pb.Port_Get{Get: true},
							},
						},
					},
				},
			},
		},
	}

	resp, err := service.doShapes(in, parser)
	assert.NoError(t, err)
	assert.Equal(t, uint64(8080), resp.Msg.GetUint64())
}

func TestDoShapes_DeletePort(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Delete port "lite"
	in := &pb.Shapes{
		Op: &pb.Shapes_Select{
			Select: &pb.Shape{
				Name: "shape1",
				Op: &pb.Shape_Ports{
					Ports: &pb.Ports{
						Op: &pb.Ports_Select{
							Select: &pb.Port{
								Name: "lite",
								Op: &pb.Port_Delete{
									Delete: true,
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := service.doShapes(in, parser)
	assert.NoError(t, err)

	// Verify deletion
	in = &pb.Shapes{
		Op: &pb.Shapes_Select{
			Select: &pb.Shape{
				Name: "shape1",
				Op: &pb.Shape_Ports{
					Ports: &pb.Ports{
						Op: &pb.Ports_List{List: true},
					},
				},
			},
		},
	}

	resp, err := service.doShapes(in, parser)
	assert.NoError(t, err)
	assert.NotContains(t, resp.Msg.GetSlice().GetValue(), "lite")
}

func TestDoShapes_GetPlugins(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Shapes{
		Op: &pb.Shapes_Select{
			Select: &pb.Shape{
				Name: "shape2",
				Op: &pb.Shape_Plugins{
					Plugins: &pb.StringSliceOp{
						Op: &pb.StringSliceOp_List{List: true},
					},
				},
			},
		},
	}

	resp, err := service.doShapes(in, parser)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"plugin1@v0.1"}, resp.Msg.GetSlice().GetValue())
}

func TestDoShapes_SetPlugins(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Set plugins
	in := &pb.Shapes{
		Op: &pb.Shapes_Select{
			Select: &pb.Shape{
				Name: "shape2",
				Op: &pb.Shape_Plugins{
					Plugins: &pb.StringSliceOp{
						Op: &pb.StringSliceOp_Set{
							Set: &pb.StringSlice{Value: []string{"plugin2@v0.2", "plugin3@v0.3"}},
						},
					},
				},
			},
		},
	}

	_, err := service.doShapes(in, parser)
	assert.NoError(t, err)

	// Verify the change
	in = &pb.Shapes{
		Op: &pb.Shapes_Select{
			Select: &pb.Shape{
				Name: "shape2",
				Op: &pb.Shape_Plugins{
					Plugins: &pb.StringSliceOp{
						Op: &pb.StringSliceOp_List{List: true},
					},
				},
			},
		},
	}

	resp, err := service.doShapes(in, parser)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"plugin2@v0.2", "plugin3@v0.3"}, resp.Msg.GetSlice().GetValue())
}

func TestDoShapes_DeletePlugin(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Delete plugin
	in := &pb.Shapes{
		Op: &pb.Shapes_Select{
			Select: &pb.Shape{
				Name: "shape2",
				Op: &pb.Shape_Plugins{
					Plugins: &pb.StringSliceOp{
						Op: &pb.StringSliceOp_Delete{
							Delete: &pb.StringSlice{Value: []string{"plugin1@v0.1"}},
						},
					},
				},
			},
		},
	}

	_, err := service.doShapes(in, parser)
	assert.NoError(t, err)

	// Verify deletion
	in = &pb.Shapes{
		Op: &pb.Shapes_Select{
			Select: &pb.Shape{
				Name: "shape2",
				Op: &pb.Shape_Plugins{
					Plugins: &pb.StringSliceOp{
						Op: &pb.StringSliceOp_List{List: true},
					},
				},
			},
		},
	}

	resp, err := service.doShapes(in, parser)
	assert.NoError(t, err)
	assert.NotContains(t, resp.Msg.GetSlice().GetValue(), "plugin1@v0.1")
}

func TestDoShapes_DeleteShape(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Delete shape "shape2"
	in := &pb.Shapes{
		Op: &pb.Shapes_Select{
			Select: &pb.Shape{
				Name: "shape2",
				Op: &pb.Shape_Delete{
					Delete: true,
				},
			},
		},
	}

	_, err := service.doShapes(in, parser)
	assert.NoError(t, err)

	// Verify deletion
	in = &pb.Shapes{Op: &pb.Shapes_List{List: true}}
	resp, err := service.doShapes(in, parser)
	assert.NoError(t, err)
	assert.NotContains(t, resp.Msg.GetSlice().GetValue(), "shape2")
}

func TestDoShapes_InvalidShapeOperation(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Invalid operation without Op
	in := &pb.Shapes{
		Op: &pb.Shapes_Select{
			Select: &pb.Shape{
				Name: "shape1",
			},
		},
	}

	_, err := service.doShapes(in, parser)
	assert.Error(t, err)
	assert.Equal(t, "invalid shapes operation", err.Error())
}

func TestDoShapes_SelectNoName(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Select operation without name
	in := &pb.Shapes{
		Op: &pb.Shapes_Select{
			Select: &pb.Shape{
				Name: "",
			},
		},
	}

	_, err := service.doShapes(in, parser)
	assert.Error(t, err)
	assert.Equal(t, "shape must have a name", err.Error())
}

func TestDoShapes_InvalidOperation(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Empty operation
	in := &pb.Shapes{}
	_, err := service.doShapes(in, parser)
	assert.Error(t, err)
	assert.Equal(t, "invalid shapes operation", err.Error())
}
