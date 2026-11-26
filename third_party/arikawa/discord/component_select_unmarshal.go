package discord

import "github.com/ayn2op/arikawa/v3/utils/json"

// The select menus' limits and default values are sent as min_values,
// max_values and default_values, which their fields marked "-" do not take
// in: these read them back from messages.

type selectLimits struct {
	MinValues     *int `json:"min_values"`
	MaxValues     *int `json:"max_values"`
	DefaultValues []struct {
		ID   Snowflake `json:"id"`
		Type string    `json:"type"`
	} `json:"default_values"`
}

func (l selectLimits) limits() [2]int {
	if l.MinValues == nil && l.MaxValues == nil {
		return [2]int{}
	}
	v := [2]int{1, 1}
	if l.MinValues != nil {
		v[0] = *l.MinValues
	}
	if l.MaxValues != nil {
		v[1] = *l.MaxValues
	}
	return v
}

func (s *StringSelectComponent) UnmarshalJSON(b []byte) error {
	type raw StringSelectComponent
	var l selectLimits
	if err := json.Unmarshal(b, (*raw)(s)); err != nil {
		return err
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return err
	}
	s.ValueLimits = l.limits()
	return nil
}

func (s *UserSelectComponent) UnmarshalJSON(b []byte) error {
	type raw UserSelectComponent
	var l selectLimits
	if err := json.Unmarshal(b, (*raw)(s)); err != nil {
		return err
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return err
	}
	s.ValueLimits = l.limits()
	for _, d := range l.DefaultValues {
		s.DefaultUsers = append(s.DefaultUsers, UserID(d.ID))
	}
	return nil
}

func (s *RoleSelectComponent) UnmarshalJSON(b []byte) error {
	type raw RoleSelectComponent
	var l selectLimits
	if err := json.Unmarshal(b, (*raw)(s)); err != nil {
		return err
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return err
	}
	s.ValueLimits = l.limits()
	for _, d := range l.DefaultValues {
		s.DefaultRoles = append(s.DefaultRoles, RoleID(d.ID))
	}
	return nil
}

func (s *MentionableSelectComponent) UnmarshalJSON(b []byte) error {
	type raw MentionableSelectComponent
	var l selectLimits
	if err := json.Unmarshal(b, (*raw)(s)); err != nil {
		return err
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return err
	}
	s.ValueLimits = l.limits()
	for _, d := range l.DefaultValues {
		if d.Type == "role" {
			s.DefaultMentions = append(s.DefaultMentions, DefaultRoleMention(RoleID(d.ID)))
		} else {
			s.DefaultMentions = append(s.DefaultMentions, DefaultUserMention(UserID(d.ID)))
		}
	}
	return nil
}

func (s *ChannelSelectComponent) UnmarshalJSON(b []byte) error {
	type raw ChannelSelectComponent
	var l selectLimits
	if err := json.Unmarshal(b, (*raw)(s)); err != nil {
		return err
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return err
	}
	s.ValueLimits = l.limits()
	for _, d := range l.DefaultValues {
		s.DefaultChannels = append(s.DefaultChannels, ChannelID(d.ID))
	}
	return nil
}

// UserID is the user of a default mention, if it is one.
func (m DefaultMention) UserID() UserID { return m.userId }

// RoleID is the role of a default mention, if it is one.
func (m DefaultMention) RoleID() RoleID { return m.roleId }
