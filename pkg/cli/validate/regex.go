package validate

const start_with_letter = `^[A-Za-z]`
const contain_letters_numbers_underscores_dashes = `^[a-zA-Z0-9_-]*$`
const is_description = `[a-zA-Z0-9_ ]$`

var NameRegex = [][]string{
	{MustStartWithALetter, start_with_letter},
	{CanOnlyContainLettersNumbersAndUnderscores, contain_letters_numbers_underscores_dashes},
}

var DescRegex = [][]string{
	{CanOnlyContainLettersNumbersSpacesAndUnderscores, is_description},
}

var TagRegex = [][]string{
	{CanOnlyContainLettersNumbersAndUnderscores, contain_letters_numbers_underscores_dashes},
}
