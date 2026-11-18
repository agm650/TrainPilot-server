package model

const SignalProfileSNCFBAL3 = "SNCF_BAL_3"

// SNCFBAL3Profile returns a fresh preset. Country-specific names and colours
// belong here, while validators and the future rules engine use semantic roles.
func SNCFBAL3Profile() SignalProfile {
	return SignalProfile{
		ID: SignalProfileSNCFBAL3, Name: "SNCF BAL trois feux",
		RuleSetID: SignalRuleSetBAL3, FallbackAspect: "S",
		Targets: []SignalTarget{{ID: "main", Name: "Cible principale"}},
		Lights: []SignalLightDefinition{
			{ID: "red", TargetID: "main", Color: "#FF0000"},
			{ID: "yellow", TargetID: "main", Color: "#FFFF00"},
			{ID: "green", TargetID: "main", Color: "#00FF00"},
		},
		Aspects: []SignalAspect{
			{ID: "S", Name: "Sémaphore", Role: SignalRoleStop, Indications: []SignalLightIndication{{LightID: "red", State: SignalLightOn}}},
			{ID: "A", Name: "Avertissement", Role: SignalRoleCaution, Indications: []SignalLightIndication{{LightID: "yellow", State: SignalLightOn}}},
			{ID: "VL", Name: "Voie libre", Role: SignalRoleProceed, Indications: []SignalLightIndication{{LightID: "green", State: SignalLightOn}}},
		},
	}
}
