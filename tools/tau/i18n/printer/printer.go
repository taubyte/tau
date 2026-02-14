package printer

import (
	"fmt"
	"io"

	"github.com/pterm/pterm"
)

// Printer is the interface for info, success, and warning output used across i18n and prompts.
// It can be replaced with a mock in tests.
type Printer interface {
	InfoPrintln(a ...any)
	InfoPrintfln(format string, a ...any)
	SuccessPrintfln(format string, a ...any)
	Warning(err error)
	WarningPrintln(a ...any)
	WarningPrintfln(format string, a ...any)
	SprintCyan(s string) string
	SprintfGreen(format string, a ...any) string
}

// Out is the global printer used by i18n. Defaults to pterm; tests can set it to a no-op or mock.
var Out Printer = ptermPrinter{}

// SetOutput sets the global printer (e.g. for tests). Returns a restore func.
func SetOutput(p Printer) (restore func()) {
	prev := Out
	Out = p
	return func() { Out = prev }
}

type ptermPrinter struct{}

func (ptermPrinter) InfoPrintln(a ...any) {
	pterm.Info.Println(a...)
}

func (ptermPrinter) InfoPrintfln(format string, a ...any) {
	pterm.Info.Printfln(format, a...)
}

func (ptermPrinter) SuccessPrintfln(format string, a ...any) {
	pterm.Success.Printfln(format, a...)
}

func (ptermPrinter) Warning(err error) {
	if err != nil {
		pterm.Warning.Println(err.Error())
	}
}

func (ptermPrinter) WarningPrintln(a ...any) {
	pterm.Warning.Println(a...)
}

func (ptermPrinter) WarningPrintfln(format string, a ...any) {
	pterm.Warning.Printfln(format, a...)
}

func (ptermPrinter) SprintCyan(s string) string {
	return pterm.FgCyan.Sprint(s)
}

func (ptermPrinter) SprintfGreen(format string, a ...any) string {
	return pterm.FgGreen.Sprintf(format, a...)
}

// Noop returns a Printer that discards all writes (for tests).
func Noop() Printer { return noopPrinter{} }

type noopPrinter struct{}

func (noopPrinter) InfoPrintln(...any) {}

func (noopPrinter) InfoPrintfln(string, ...any) {}

func (noopPrinter) SuccessPrintfln(string, ...any) {}

func (noopPrinter) Warning(error) {}

func (noopPrinter) WarningPrintln(...any) {}

func (noopPrinter) WarningPrintfln(string, ...any) {}

func (noopPrinter) SprintCyan(s string) string { return s }

func (noopPrinter) SprintfGreen(format string, a ...any) string {
	return fmt.Sprintf(format, a...)
}

// WriterOutput returns a Printer that writes info/success lines to w (e.g. for tests that capture output).
func WriterOutput(w io.Writer) Printer {
	return &writerPrinter{w: w}
}

type writerPrinter struct {
	w io.Writer
}

func (w *writerPrinter) InfoPrintln(a ...any) {
	fmt.Fprintln(w.w, a...)
}

func (w *writerPrinter) InfoPrintfln(format string, a ...any) {
	fmt.Fprintf(w.w, format+"\n", a...)
}

func (w *writerPrinter) SuccessPrintfln(format string, a ...any) {
	fmt.Fprintf(w.w, format+"\n", a...)
}

func (w *writerPrinter) Warning(err error) {
	if err != nil {
		fmt.Fprintln(w.w, err.Error())
	}
}

func (w *writerPrinter) WarningPrintln(a ...any) {
	fmt.Fprintln(w.w, a...)
}

func (w *writerPrinter) WarningPrintfln(format string, a ...any) {
	fmt.Fprintf(w.w, format+"\n", a...)
}

func (w *writerPrinter) SprintCyan(s string) string { return s }

func (w *writerPrinter) SprintfGreen(format string, a ...any) string {
	return fmt.Sprintf(format, a...)
}

func SuccessWithName(format, prefix, name string) {
	Out.SuccessPrintfln(format, prefix, Out.SprintCyan(name))
}

func SuccessWithNameOnCloud(format, prefix, name, cloud string) {
	Out.SuccessPrintfln(format, prefix, Out.SprintCyan(name), Out.SprintCyan(cloud))
}
