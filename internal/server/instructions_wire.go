package server

// putInstructionsRequest replaces both global instruction fields.
type putInstructionsRequest struct {
	AboutMe *string `json:"about_me"`
	Rules   *string `json:"rules"`
}

// projectInstructionsBody contains the user's project instruction text.
type projectInstructionsBody struct {
	Instructions string `json:"instructions"`
}

// putProjectInstructionsRequest requires explicit instruction text, including empty text.
type putProjectInstructionsRequest struct {
	Instructions *string `json:"instructions"`
}
