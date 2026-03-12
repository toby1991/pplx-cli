#ifndef AX_H
#define AX_H

// Activate App to foreground. Returns 0 on success, -1 on failure.
int  ax_activate_app(const char *bundle_id);

// Find a button by exact AXDescription. Returns AXUIElementRef (caller must ax_release_element).
void* ax_find_button(const char *bundle_id, const char *description);

// Find a button by AXDescription prefix. Returns AXUIElementRef (caller must ax_release_element).
void* ax_find_button_prefix(const char *bundle_id, const char *prefix);

// Find a checkbox by AXDescription prefix. Returns AXUIElementRef (caller must ax_release_element).
void* ax_find_checkbox_prefix(const char *bundle_id, const char *prefix);

// Get AXCheckBox value: 1=checked, 0=unchecked, -1=error.
int  ax_get_checkbox_value(void *element_ref);

// Perform AXPressAction on element. Returns 0 on success, -1 on failure.
int  ax_click(void *element_ref);

// Release an AXUIElementRef.
void ax_release_element(void *element_ref);

// Read clipboard string. Returns malloc'd C string (caller must free), NULL on failure.
char* ax_read_clipboard(void);

// Write string to clipboard.
void ax_write_clipboard(const char *text);

// Return clipboard changeCount (monotonically increasing on each content change).
long ax_clipboard_change_count(void);

// Read all AXTextArea values from the app window, concatenated with newlines.
// Returns malloc'd C string (caller must free), NULL on failure.
// Used as fallback when copy button doesn't update clipboard.
char* ax_read_all_textarea_values(const char *bundle_id);

// Poll for a button by AXDescription until timeout. Returns AXUIElementRef or NULL.
void* ax_wait_for_button(const char *bundle_id, const char *description, int timeout_ms, int poll_ms);

// Open file dialog and type path. Returns 0 on success.
int  ax_open_file_dialog_and_input(const char *path);

// Read a UserDefaults string value. Returns malloc'd C string (caller must free), NULL on failure.
char* ax_read_defaults(const char *app_id, const char *key);

// Check if current process is AX trusted. Returns 1 if trusted, 0 otherwise.
int  ax_is_trusted(void);

// Wait for an AXWindow to appear. Returns 0 on success, -1 on timeout.
int  ax_wait_for_window(const char *bundle_id, int timeout_ms, int poll_ms);

// Dump all AX elements (role + description + value) to stderr for debugging.
void ax_dump_elements(const char *bundle_id);

// Dump detailed window info: count, properties, children count, first-level tree.
void ax_dump_window_info(const char *bundle_id);

// Scroll the Perplexity scroll area down by one page.
// Returns 0 on success, -1 if scrollable group not found or scroll failed.
int ax_scroll_to_bottom(const char *bundle_id);

// Return total character count of all AXTextArea values in the app's visible AX tree.
// Increases while Perplexity is still generating; stable when generation is complete.
int ax_get_content_length(const char *bundle_id);

// Return the number of connected displays (NSScreen count).
// Returns 0 if no display is available (headless without virtual display).
int ax_display_count(void);

// Check if a button with the given AXDescription exists in the app's AX tree.
// Returns 1 if found, 0 if not found. Lightweight — does not return the element ref.
int ax_has_button(const char *bundle_id, const char *description);

// Check if App's main window has a direct AXPopover child (sources/model selector open).
// Returns 1 if a popover is present, 0 otherwise.
int ax_has_popover(const char *bundle_id);

// Send Escape key to the foreground App (closes popovers / dismisses overlays).
void ax_press_escape(void);

#endif // AX_H
