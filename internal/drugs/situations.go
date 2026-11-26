package drugs

import "strings"

// Situation is an everyday problem a non-prescription medicine's label may
// say it treats. Terms are matched (with English stemming) against the
// label's purpose and uses, never against warnings.
type Situation struct {
	Slug     string   `json:"slug"`
	Label    string   `json:"label"`
	Category string   `json:"category"`
	Terms    []string `json:"terms"`
}

func (s Situation) ftsQuery() string {
	var q []string
	for _, t := range s.Terms {
		q = append(q, `"`+strings.ReplaceAll(t, `"`, "")+`"`)
	}
	return strings.Join(q, " OR ")
}

// Situations is Arcana's list, grouped for display. It was written for
// Arcana from the wording common on FDA "Drug Facts" panels.
var Situations = []Situation{
	{"pain", "Pain", "Pain and fever", []string{"pain reliever", "minor aches and pains", "temporarily relieves minor aches", "analgesic"}},
	{"headache", "Headache", "Pain and fever", []string{"headache", "migraine"}},
	{"fever", "Fever", "Pain and fever", []string{"fever reducer", "reduces fever", "temporarily reduces fever"}},
	{"muscle-joint-pain", "Muscle or joint pain", "Pain and fever", []string{"muscular aches", "backache", "arthritis", "joint pain", "sprains", "strains"}},
	{"menstrual-cramps", "Menstrual cramps", "Pain and fever", []string{"menstrual cramps", "premenstrual", "menstrual periods"}},
	{"toothache", "Toothache or mouth sores", "Pain and fever", []string{"toothache", "canker sores", "sore gums", "teething"}},

	{"cough", "Cough", "Cold, flu and allergy", []string{"cough suppressant", "cough", "expectorant"}},
	{"congestion", "Stuffy nose or sinus pressure", "Cold, flu and allergy", []string{"nasal congestion", "sinus congestion", "nasal decongestant", "sinus pressure", "stuffy nose"}},
	{"sore-throat", "Sore throat", "Cold, flu and allergy", []string{"sore throat", "throat irritation"}},
	{"runny-nose", "Runny nose and sneezing", "Cold, flu and allergy", []string{"runny nose", "sneezing"}},
	{"allergy", "Hay fever and allergies", "Cold, flu and allergy", []string{"hay fever", "upper respiratory allergies", "allergic rhinitis", "itchy watery eyes", "antihistamine"}},
	{"cold", "Common cold or flu", "Cold, flu and allergy", []string{"common cold", "cold symptoms", "cold and flu", "flu symptoms"}},

	{"heartburn", "Heartburn or acid indigestion", "Stomach and digestion", []string{"heartburn", "acid indigestion", "sour stomach", "acid reducer", "antacid"}},
	{"upset-stomach", "Upset stomach", "Stomach and digestion", []string{"upset stomach", "indigestion"}},
	{"diarrhea", "Diarrhea", "Stomach and digestion", []string{"diarrhea", "antidiarrheal"}},
	{"constipation", "Constipation", "Stomach and digestion", []string{"constipation", "laxative", "irregularity"}},
	{"gas", "Gas and bloating", "Stomach and digestion", []string{"gas", "bloating", "flatulence"}},
	{"nausea", "Nausea, vomiting or motion sickness", "Stomach and digestion", []string{"nausea", "vomiting", "motion sickness"}},
	{"hemorrhoids", "Hemorrhoids", "Stomach and digestion", []string{"hemorrhoids", "hemorrhoidal"}},

	{"burn", "Minor burns and sunburn", "Skin and first aid", []string{"minor burns", "sunburn pain", "burns"}},
	{"cuts", "Cuts and scrapes", "Skin and first aid", []string{"minor cuts", "scrapes", "first aid to help prevent", "first aid antiseptic", "wound"}},
	{"itch", "Itching or rash", "Skin and first aid", []string{"itching", "rashes", "skin irritation", "eczema"}},
	{"bites", "Insect bites and stings", "Skin and first aid", []string{"insect bites", "stings", "bug bites"}},
	{"poison-ivy", "Poison ivy, oak or sumac", "Skin and first aid", []string{"poison ivy", "poison oak", "poison sumac"}},
	{"fungal", "Athlete's foot, ringworm or jock itch", "Skin and first aid", []string{"athletes foot", "athlete's foot", "ringworm", "jock itch", "antifungal"}},
	{"acne", "Acne", "Skin and first aid", []string{"acne", "pimples", "blemishes"}},
	{"cold-sores", "Cold sores", "Skin and first aid", []string{"cold sores", "fever blisters"}},
	{"diaper-rash", "Diaper rash", "Skin and first aid", []string{"diaper rash"}},
	{"dry-skin", "Dry, chapped or cracked skin", "Skin and first aid", []string{"dry skin", "chapped skin", "cracked skin", "chapped lips"}},
	{"sunscreen", "Preventing sunburn", "Skin and first aid", []string{"helps prevent sunburn", "sunscreen"}},
	{"hand-hygiene", "Hand sanitizing", "Skin and first aid", []string{"hand sanitizer", "decrease bacteria on the skin", "hand sanitizing"}},

	{"sleep", "Occasional sleeplessness", "Other", []string{"sleeplessness", "sleep aid", "difficulty falling asleep"}},
	{"eyes", "Red or irritated eyes", "Other", []string{"redness of the eye", "eye irritation", "minor eye irritation", "dry eye"}},
	// Non-prescription ear products treat earwax or water in the ear; labels
	// that name earache are mostly homeopathic, which this search leaves out.
	{"ears", "Earwax, water in the ear or earache", "Other", []string{"earwax", "ear wax", "water in the ears", "water-clogged ears", "swimmer's ear", "earache", "ear pain"}},
	// Oral rehydration solutions are not drugs and have no label here; the
	// remedies list covers them. Salt tablets name heat cramps. The word
	// "dehydration" itself is left out: stemmed, it matches moisturizers for
	// "dehydrated skin" and an insulin label.
	{"dehydration", "Dehydration or heat cramps", "Other", []string{"heat cramps", "electrolyte replenisher"}},
	{"pinworm", "Pinworm", "Other", []string{"pinworm"}},
	{"smoking", "Quitting smoking", "Other", []string{"stop smoking", "quit smoking", "nicotine withdrawal"}},
	{"lice", "Head lice", "Other", []string{"head lice", "lice"}},
	{"yeast", "Vaginal yeast infection", "Other", []string{"vaginal yeast infection", "vaginal yeast"}},
}

// SituationBySlug finds a situation.
func SituationBySlug(slug string) (Situation, bool) {
	for _, s := range Situations {
		if s.Slug == slug {
			return s, true
		}
	}
	return Situation{}, false
}
