// Code generated by go-swagger; DO NOT EDIT.

package models

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the swagger generate command

import (
	strfmt "github.com/go-openapi/strfmt"

	"github.com/go-openapi/errors"
	"github.com/go-openapi/swag"
)

// LabelConfigurationSpec User desired Label configuration of an endpoint
// swagger:model LabelConfigurationSpec

type LabelConfigurationSpec struct {

	// Labels derived from orchestration system which have been disabled.
	Disabled Labels `json:"disabled"`

	// Custom labels in addition to orchestration system labels.
	User Labels `json:"user"`
}

/* polymorph LabelConfigurationSpec disabled false */

/* polymorph LabelConfigurationSpec user false */

// Validate validates this label configuration spec
func (m *LabelConfigurationSpec) Validate(formats strfmt.Registry) error {
	var res []error

	if len(res) > 0 {
		return errors.CompositeValidationError(res...)
	}
	return nil
}

// MarshalBinary interface implementation
func (m *LabelConfigurationSpec) MarshalBinary() ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	return swag.WriteJSON(m)
}

// UnmarshalBinary interface implementation
func (m *LabelConfigurationSpec) UnmarshalBinary(b []byte) error {
	var res LabelConfigurationSpec
	if err := swag.ReadJSON(b, &res); err != nil {
		return err
	}
	*m = res
	return nil
}
