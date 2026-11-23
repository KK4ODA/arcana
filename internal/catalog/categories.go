package catalog

// Category slugs. The list is ordered for display and is extensible: unknown
// categories from a provider fall back to CategoryReference rather than being
// rejected.
const (
	CategoryReference    = "reference"
	CategoryMedicine     = "medicine"
	CategoryFirstAid     = "first-aid"
	CategoryPractical    = "practical-skills"
	CategoryAgriculture  = "agriculture"
	CategoryWater        = "water-sanitation"
	CategoryEnergy       = "energy-electrical"
	CategoryMechanical   = "mechanical"
	CategoryRadio        = "radio-communications"
	CategoryMaps         = "maps-navigation"
	CategoryPreparedness = "preparedness"
	CategoryEducation    = "education"
	CategoryComputing    = "computing"
	CategoryScience      = "science-technical"
	CategoryHistory      = "history-culture"
	CategoryPersonal     = "personal-community"
	CategoryAssistant    = "assistant"
)

// CategoryInfo describes a category for the UI.
type CategoryInfo struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Categories lists the built-in categories in display order.
var Categories = []CategoryInfo{
	{CategoryReference, "Encyclopedias and reference"},
	{CategoryMedicine, "Medicine and public health"},
	{CategoryFirstAid, "First aid and emergency care"},
	{CategoryPractical, "Practical skills and repair"},
	{CategoryAgriculture, "Agriculture and food production"},
	{CategoryWater, "Water and sanitation"},
	{CategoryEnergy, "Energy and electrical systems"},
	{CategoryMechanical, "Mechanical engineering"},
	{CategoryRadio, "Amateur radio and communications"},
	{CategoryMaps, "Maps and navigation"},
	{CategoryPreparedness, "Survival and emergency preparedness"},
	{CategoryEducation, "Education and training"},
	{CategoryComputing, "Computing and programming"},
	{CategoryScience, "Scientific and technical reference"},
	{CategoryHistory, "History, culture and literature"},
	{CategoryPersonal, "Personal and community documents"},
	{CategoryAssistant, "Question answering (local AI models)"},
}

var categorySet = func() map[string]bool {
	m := map[string]bool{}
	for _, c := range Categories {
		m[c.ID] = true
	}
	return m
}()

// IsCategory reports whether id is a known category.
func IsCategory(id string) bool { return categorySet[id] }
