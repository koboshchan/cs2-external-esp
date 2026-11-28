// Objective-C bridge for the macOS viewer: a native launcher window and a
// transparent, click-through overlay window.
//
// Ebiten cannot do per-pixel transparency, so the macOS build draws with Core
// Graphics into an offscreen bitmap and presents it as the layer contents of a
// borderless clear window.
//
// Go polls the flags (connect/start/refresh/quit) instead of using callbacks,
// which keeps the cgo surface small.

#import <Cocoa/Cocoa.h>
#import <CoreText/CoreText.h>
#import <QuartzCore/QuartzCore.h>
#import <ImageIO/ImageIO.h>
#import <Carbon/Carbon.h>

#include <string.h>
#include <unistd.h>

// ---------------------------------------------------------------- state

static NSPanel *gConsole = nil;
static NSTableView *gViewPlayersTable = nil;
static NSMutableArray<NSDictionary<NSString *, NSString *> *> *gViewPlayers = nil;
static NSTextField *gViewStatus = nil;
static BOOL gUpdatingViewPlayers = NO;
static char gViewKey[256];
static void ov_view_selection_changed(void);
static NSTextField *makeLabel(NSRect frame, NSString *text);
static NSButton *gDisplayChecks[5];
static NSButton *gOverlayToggleButton = nil;
static NSButton *gOverlayHotkeyButton = nil;
static NSTextField *gOverlayHotkeyStatus = nil;
static EventHotKeyRef gOverlayHotkey = NULL;
static UInt32 gOverlayHotkeyID = 2;
static UInt32 gOverlayKeyCode = kVK_F6;
static UInt32 gOverlayKeyModifiers = 0;
static NSString *gOverlayKeyTitle = @"F6";
static BOOL gRecordingOverlayHotkey = NO;
static BOOL gOverlayHotkeyDown = NO;
static id gOverlayKeyMonitor = nil;
static NSString *const kOverlayHotkeySetting = @"OverlayMasterHotkey";
static void ov_cancel_hotkey_recording(void);
static BOOL ov_bind_overlay_hotkey(UInt32 code, UInt32 modifiers, NSString *title);
static NSSlider *gOverlayOpacity = nil;
static NSSlider *gConsoleOpacity = nil;
static NSTextField *gOpacityLabels[2];
static NSSlider *gDisplayDelay = nil;
static NSTextField *gDisplayDelayLabel = nil;
static NSString *const kDisplayDelay = @"DisplaySyncDelayMS";
static NSColorWell *gPlayerColors[5];
static const uint32_t gDefaultPlayerColors[5] = {0xeb9632, 0x50a0ff, 0x50dc78, 0xeb5a50, 0xebebf0};
static NSString *const kOverlayOpacity = @"AppearanceOverlayOpacity";
static NSString *const kConsoleOpacity = @"AppearanceConsoleOpacity";
static NSString *const kPlayerColors = @"AppearancePlayerColors";
static void ov_apply_appearance(void);
uint32_t ov_controls_color(int index);
static EventHotKeyRef gConsoleHotKey = NULL;
static EventHandlerRef gConsoleHandler = NULL;
static BOOL gConsoleToggle = NO;
static BOOL gMappingActive = NO;
static NSRunningApplication *gReturnApplication = nil;
static void ov_console_toggle(void);

static NSWindow *gLauncher = nil;
static NSTextField *gServerField = nil;
static NSTextField *gRoomField = nil;
static NSTextField *gKeyField = nil;
static NSTextField *gStatusLabel = nil;
static NSButton *gFollowGeForce = nil;
static NSPopUpButton *gStreamAspect = nil;
static NSTextField *gTopInset = nil;
static NSPopUpButton *gPlayerPopup = nil;
static NSPopUpButton *gWindowPopup = nil;
static NSTextField *gWindowLabel = nil;
static NSButton *gWindowRefresh = nil;
static NSTextField *gAutomaticTargetLabel = nil;

static BOOL gConnectFlag = NO;
static BOOL gStartFlag = NO;
static BOOL gRefreshFlag = NO;
static BOOL gQuitFlag = NO;
static BOOL gShowLauncherFlag = NO;

static NSPanel *gOverlay = nil;
static NSView *gOverlayView = nil;
static CGContextRef gCtx = NULL;
static double gW = 0, gH = 0, gScale = 1;
static BOOL gOverlayVisible = NO;
static BOOL gOverlayHidden = NO;

static NSStatusItem *gStatusItem = nil;
static id gController = nil;

// Window list for the picker.
static NSMutableArray<NSNumber *> *gWindowNumbers = nil;
static NSMutableArray<NSValue *> *gWindowBounds = nil;
static char gWindowTitles[16384];

// ---------------------------------------------------------------- helpers

static NSString *const kOvFontName = @"Menlo";

static NSWindowCollectionBehavior ov_floating_collection_behavior(void) {
  NSWindowCollectionBehavior behavior = NSWindowCollectionBehaviorCanJoinAllSpaces |
      NSWindowCollectionBehaviorFullScreenAuxiliary;
  if (@available(macOS 13.0, *)) {
    behavior |= NSWindowCollectionBehaviorCanJoinAllApplications;
  }
  return behavior;
}

// Controls that react to the very first click, even when the app is not the
// active application yet. Without this, macOS swallows the first click to
// activate the window and the popup/button appears dead.
@interface OvButton : NSButton
@end

@implementation OvButton
- (BOOL)acceptsFirstMouse:(NSEvent *)event { return YES; }
@end

@interface OvHotkeyButton : OvButton
@end

@implementation OvHotkeyButton
- (BOOL)acceptsFirstResponder { return YES; }
- (BOOL)resignFirstResponder {
  ov_cancel_hotkey_recording();
  return [super resignFirstResponder];
}
- (BOOL)performKeyEquivalent:(NSEvent *)event {
  if (!gRecordingOverlayHotkey) return [super performKeyEquivalent:event];
  [self keyDown:event];
  return YES;
}
- (void)keyDown:(NSEvent *)event {
  if (!gRecordingOverlayHotkey) { [super keyDown:event]; return; }
  if (event.isARepeat) return;
  if (event.keyCode == kVK_Escape) { ov_cancel_hotkey_recording(); return; }
  NSEventModifierFlags flags = event.modifierFlags;
  UInt32 modifiers = 0;
  NSMutableString *title = [NSMutableString string];
  if (flags & NSEventModifierFlagControl) { modifiers |= controlKey; [title appendString:@"⌃"]; }
  if (flags & NSEventModifierFlagOption) { modifiers |= optionKey; [title appendString:@"⌥"]; }
  if (flags & NSEventModifierFlagShift) { modifiers |= shiftKey; [title appendString:@"⇧"]; }
  if (flags & NSEventModifierFlagCommand) { modifiers |= cmdKey; [title appendString:@"⌘"]; }
  NSDictionary *names = @{@(kVK_F1):@"F1", @(kVK_F2):@"F2", @(kVK_F3):@"F3", @(kVK_F4):@"F4",
      @(kVK_F5):@"F5", @(kVK_F6):@"F6", @(kVK_F7):@"F7", @(kVK_F8):@"F8",
      @(kVK_F9):@"F9", @(kVK_F10):@"F10", @(kVK_F11):@"F11", @(kVK_F12):@"F12",
      @(kVK_F13):@"F13", @(kVK_F14):@"F14", @(kVK_F15):@"F15", @(kVK_F16):@"F16",
      @(kVK_F17):@"F17", @(kVK_F18):@"F18", @(kVK_F19):@"F19", @(kVK_F20):@"F20",
      @(kVK_Space):@"Space", @(kVK_Return):@"Return", @(kVK_Tab):@"Tab",
      @(kVK_Delete):@"Delete", @(kVK_ForwardDelete):@"Forward Delete",
      @(kVK_LeftArrow):@"←", @(kVK_RightArrow):@"→", @(kVK_UpArrow):@"↑", @(kVK_DownArrow):@"↓"};
  NSString *name = names[@(event.keyCode)] ?: event.charactersIgnoringModifiers.uppercaseString;
  if (!name.length || [name rangeOfCharacterFromSet:NSCharacterSet.controlCharacterSet].location != NSNotFound)
    name = [NSString stringWithFormat:@"Key %u", event.keyCode];
  [title appendString:name];
  if (ov_bind_overlay_hotkey(event.keyCode, modifiers, title)) {
    ov_cancel_hotkey_recording();
    [self.window makeFirstResponder:gViewPlayersTable];
  }
}
@end

@interface OvPopUpButton : NSPopUpButton
@end

@implementation OvPopUpButton
- (BOOL)acceptsFirstMouse:(NSEvent *)event { return YES; }
@end

@interface OvPlayersTable : NSTableView
@end

@implementation OvPlayersTable
- (BOOL)acceptsFirstMouse:(NSEvent *)event { return YES; }
@end

@interface OvSlider : NSSlider
@end

@implementation OvSlider
- (BOOL)acceptsFirstMouse:(NSEvent *)event { return YES; }
@end

@interface OvColorWell : NSColorWell
@end

@implementation OvColorWell
- (BOOL)acceptsFirstMouse:(NSEvent *)event { return YES; }
- (void)activate:(BOOL)exclusive {
  [super activate:exclusive];
  NSColorPanel *panel = NSColorPanel.sharedColorPanel;
  panel.level = NSFloatingWindowLevel + 2;
  panel.collectionBehavior = ov_floating_collection_behavior();
  panel.showsAlpha = NO;
  [panel makeKeyAndOrderFront:nil];
}
@end

@interface OvConsolePanel : NSPanel
@end

@implementation OvConsolePanel
- (BOOL)canBecomeKeyWindow { return YES; }
- (BOOL)canBecomeMainWindow { return NO; }
@end

@interface OvController : NSObject <NSWindowDelegate, NSTableViewDataSource, NSTableViewDelegate>
- (void)connectClicked:(id)sender;
- (void)startClicked:(id)sender;
- (void)refreshClicked:(id)sender;
- (void)showLauncher:(id)sender;
- (void)quitClicked:(id)sender;
- (void)toggleConsole:(id)sender;
- (void)appearanceChanged:(id)sender;
- (void)displayDelayChanged:(id)sender;
- (void)resetAppearance:(id)sender;
- (void)toggleOverlay:(id)sender;
- (void)recordOverlayHotkey:(id)sender;
@end

@implementation OvController

- (NSInteger)numberOfRowsInTableView:(NSTableView *)tableView {
  return gViewPlayers.count;
}

- (NSView *)tableView:(NSTableView *)tableView viewForTableColumn:(NSTableColumn *)column row:(NSInteger)row {
  if (row < 0 || row >= (NSInteger)gViewPlayers.count) return nil;
  NSTableCellView *cell = [tableView makeViewWithIdentifier:@"viewPlayer" owner:nil];
  if (!cell) {
    cell = [[NSTableCellView alloc] initWithFrame:NSMakeRect(0, 0, column.width, 24)];
    cell.identifier = @"viewPlayer";
    NSTextField *label = makeLabel(NSMakeRect(6, 2, column.width - 12, 20), @"");
    label.lineBreakMode = NSLineBreakByTruncatingTail;
    label.autoresizingMask = NSViewWidthSizable;
    [cell addSubview:label];
    cell.textField = label;
  }
  cell.textField.stringValue = gViewPlayers[row][@"title"];
  cell.toolTip = cell.textField.stringValue;
  return cell;
}

- (void)tableViewSelectionDidChange:(NSNotification *)notification {
  if (notification.object == gViewPlayersTable && !gUpdatingViewPlayers) ov_view_selection_changed();
}

- (void)appearanceChanged:(id)sender {
  ov_apply_appearance();
  NSUserDefaults *defaults = NSUserDefaults.standardUserDefaults;
  [defaults setDouble:gOverlayOpacity.doubleValue forKey:kOverlayOpacity];
  [defaults setDouble:gConsoleOpacity.doubleValue forKey:kConsoleOpacity];
  NSMutableArray<NSNumber *> *colors = [NSMutableArray array];
  for (int i = 0; i < 5; ++i) [colors addObject:@(ov_controls_color(i))];
  [defaults setObject:colors forKey:kPlayerColors];
}

- (void)displayDelayChanged:(id)sender {
  NSInteger delay = lround(gDisplayDelay.doubleValue);
  gDisplayDelay.integerValue = delay;
  gDisplayDelayLabel.stringValue = [NSString stringWithFormat:@"%ld ms", (long)delay];
  [NSUserDefaults.standardUserDefaults setInteger:delay forKey:kDisplayDelay];
}

- (void)resetAppearance:(id)sender {
  gOverlayOpacity.doubleValue = 100;
  gConsoleOpacity.doubleValue = 100;
  for (NSUInteger i = 0; i < 5; ++i) {
    uint32_t rgb = gDefaultPlayerColors[i];
    gPlayerColors[i].color = [NSColor colorWithSRGBRed:((rgb >> 16) & 255) / 255.0
        green:((rgb >> 8) & 255) / 255.0 blue:(rgb & 255) / 255.0 alpha:1];
  }
  [self appearanceChanged:sender];
}

- (void)toggleConsole:(id)sender { ov_console_toggle(); }
- (void)toggleOverlay:(id)sender {
  gOverlayHidden = !gOverlayHidden;
  gOverlayToggleButton.title = gOverlayHidden ? @"显示映射" : @"隐藏映射";
  if (gOverlayHidden) {
    [gOverlay orderOut:nil];
    gOverlayVisible = NO;
  }
}
- (void)recordOverlayHotkey:(id)sender {
  if (gRecordingOverlayHotkey) { ov_cancel_hotkey_recording(); return; }
  [gConsole makeFirstResponder:gOverlayHotkeyButton];
  gRecordingOverlayHotkey = YES;
  gOverlayHotkeyButton.title = @"按下快捷键…";
  gOverlayHotkeyStatus.stringValue = @"支持组合键，Esc 取消。";
}
- (BOOL)windowShouldClose:(NSWindow *)sender {
  if (sender == gConsole) { ov_console_toggle(); return NO; }
  return YES;
}
- (void)connectClicked:(id)sender { gConnectFlag = YES; }
- (void)startClicked:(id)sender { gStartFlag = YES; }
- (void)refreshClicked:(id)sender { gRefreshFlag = YES; }
- (void)followGeForceChanged:(id)sender {
  BOOL automatic = gFollowGeForce.state == NSControlStateValueOn;
  gWindowLabel.hidden = automatic;
  gWindowPopup.hidden = automatic;
  gWindowRefresh.hidden = automatic;
  gAutomaticTargetLabel.hidden = !automatic;
}

- (void)showLauncher:(id)sender {
  if (!gLauncher) return;

  // The overlay is above menu-window level; leaving it up would hide the
  // launcher and any popup menus.
  if (gOverlay) [gOverlay orderOut:nil];
  gOverlayVisible = NO;
  gShowLauncherFlag = YES;

  [gLauncher makeKeyAndOrderFront:nil];
  [NSApp activateIgnoringOtherApps:YES];
  [gLauncher makeKeyWindow];
}

- (void)prevPlayer:(id)sender {
  NSInteger index = gPlayerPopup.indexOfSelectedItem;
  if (index > 0) [gPlayerPopup selectItemAtIndex:index - 1];
}

- (void)nextPlayer:(id)sender {
  NSInteger index = gPlayerPopup.indexOfSelectedItem;
  if (index >= 0 && index + 1 < (NSInteger)gPlayerPopup.numberOfItems) {
    [gPlayerPopup selectItemAtIndex:index + 1];
  }
}

- (void)quitClicked:(id)sender { gQuitFlag = YES; }

@end

static NSTextField *makeLabel(NSRect frame, NSString *text) {
  NSTextField *label = [[NSTextField alloc] initWithFrame:frame];
  label.stringValue = text;
  label.bezeled = NO;
  label.drawsBackground = NO;
  label.editable = NO;
  label.selectable = NO;
  return label;
}

static NSTextField *makeField(NSRect frame, NSString *value) {
  NSTextField *field = [[NSTextField alloc] initWithFrame:frame];
  field.stringValue = value ?: @"";
  field.font = [NSFont monospacedSystemFontOfSize:12 weight:NSFontWeightRegular];
  return field;
}

static OSStatus ov_console_hotkey(EventHandlerCallRef handler, EventRef event, void *data) {
  (void)handler; (void)data;
  EventHotKeyID key;
  if (GetEventParameter(event, kEventParamDirectObject, typeEventHotKeyID,
                        NULL, sizeof(key), NULL, &key) != noErr || key.signature != 'CVRW')
    return eventNotHandledErr;
  if (key.id == 1) {
    if (GetEventKind(event) != kEventHotKeyPressed) return noErr;
    if (gRecordingOverlayHotkey) gOverlayHotkeyStatus.stringValue = @"⌘P 用于控制台，请选择其他按键。";
    else gConsoleToggle = YES;
  } else if (key.id == gOverlayHotkeyID && gMappingActive) {
    if (GetEventKind(event) == kEventHotKeyReleased) { gOverlayHotkeyDown = NO; return noErr; }
    if (gRecordingOverlayHotkey) { ov_cancel_hotkey_recording(); gOverlayHotkeyDown = YES; return noErr; }
    if (!gOverlayHotkeyDown) [gController toggleOverlay:nil];
    gOverlayHotkeyDown = YES;
  } else return eventNotHandledErr;
  return noErr;
}

static void ov_cancel_hotkey_recording(void) {
  gRecordingOverlayHotkey = NO;
  gOverlayHotkeyButton.title = gOverlayKeyTitle;
}

static BOOL ov_register_overlay_hotkey(UInt32 code, UInt32 modifiers) {
  UInt32 nextID = gOverlayHotkeyID == 2 ? 3 : 2;
  EventHotKeyRef candidate = NULL;
  EventHotKeyID key = {'CVRW', nextID};
  OSStatus status = RegisterEventHotKey(code, modifiers, key, GetApplicationEventTarget(), 0, &candidate);
  if (status != noErr) {
    gOverlayHotkeyStatus.stringValue = @"该快捷键被占用或不可用，请重新绑定。";
    return NO;
  }
  if (gOverlayHotkey) UnregisterEventHotKey(gOverlayHotkey);
  gOverlayHotkey = candidate;
  gOverlayHotkeyID = nextID;
  gOverlayHotkeyDown = NO;
  return YES;
}

static BOOL ov_bind_overlay_hotkey(UInt32 code, UInt32 modifiers, NSString *title) {
  if (code == kVK_ANSI_P && modifiers == cmdKey) {
    gOverlayHotkeyStatus.stringValue = @"⌘P 用于控制台，请选择其他按键。";
    return NO;
  }
  if (gMappingActive && (!gOverlayHotkey || code != gOverlayKeyCode || modifiers != gOverlayKeyModifiers) &&
      !ov_register_overlay_hotkey(code, modifiers)) return NO;
  gOverlayKeyCode = code;
  gOverlayKeyModifiers = modifiers;
  gOverlayKeyTitle = [title copy];
  [NSUserDefaults.standardUserDefaults setObject:@{@"code":@(code), @"modifiers":@(modifiers), @"title":title}
      forKey:kOverlayHotkeySetting];
  gOverlayHotkeyStatus.stringValue = @"快捷键已保存，按一下切换全部映射。";
  return YES;
}

static NSEvent *ov_overlay_key_event(NSEvent *event) {
  if (!gMappingActive || !gOverlayHotkey || gRecordingOverlayHotkey || event.keyCode != gOverlayKeyCode) return event;
  NSEventModifierFlags flags = event.modifierFlags;
  UInt32 modifiers = 0;
  if (flags & NSEventModifierFlagControl) modifiers |= controlKey;
  if (flags & NSEventModifierFlagOption) modifiers |= optionKey;
  if (flags & NSEventModifierFlagShift) modifiers |= shiftKey;
  if (flags & NSEventModifierFlagCommand) modifiers |= cmdKey;
  if (event.type == NSEventTypeKeyUp) {
    gOverlayHotkeyDown = NO;
    return modifiers == gOverlayKeyModifiers ? nil : event;
  }
  if (modifiers != gOverlayKeyModifiers) return event;
  if (!event.isARepeat && !gOverlayHotkeyDown) [gController toggleOverlay:nil];
  gOverlayHotkeyDown = YES;
  return nil;
}

static void ov_apply_appearance(void) {
  gOverlay.alphaValue = gOverlayOpacity.doubleValue / 100;
  gConsole.alphaValue = gConsoleOpacity.doubleValue / 100;
  gOpacityLabels[0].stringValue = [NSString stringWithFormat:@"%.0f%%", gOverlayOpacity.doubleValue];
  gOpacityLabels[1].stringValue = [NSString stringWithFormat:@"%.0f%%", gConsoleOpacity.doubleValue];
}

static void ov_console_create(void) {
  if (gConsole) return;
  gConsole = [[OvConsolePanel alloc] initWithContentRect:NSMakeRect(0, 0, 440, 744)
      styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskUtilityWindow |
                NSWindowStyleMaskNonactivatingPanel
      backing:NSBackingStoreBuffered defer:NO];
  gConsole.title = @"显示控制台 · ⌘P";
  gConsole.releasedWhenClosed = NO;
  gConsole.hidesOnDeactivate = NO;
  gConsole.becomesKeyOnlyIfNeeded = NO;
  gConsole.delegate = gController;
  gConsole.level = NSFloatingWindowLevel + 1;
  gConsole.collectionBehavior = ov_floating_collection_behavior();
  NSArray<NSString *> *titles = @[@"人物方框", @"血条", @"骨骼", @"人物名称与状态", @"队友"];
  for (NSUInteger i = 0; i < titles.count; ++i) {
    NSButton *check = [[OvButton alloc] initWithFrame:NSMakeRect(20, 692 - i * 34, 400, 26)];
    [check setButtonType:NSButtonTypeSwitch];
    check.title = titles[i];
    check.state = NSControlStateValueOn;
    [gConsole.contentView addSubview:check];
    gDisplayChecks[i] = check;
  }
  [gConsole.contentView addSubview:makeLabel(NSMakeRect(20, 522, 400, 24), @"骨骼世界视角")];
  NSScrollView *playersScroll = [[NSScrollView alloc] initWithFrame:NSMakeRect(20, 414, 400, 100)];
  playersScroll.hasVerticalScroller = YES;
  playersScroll.borderType = NSBezelBorder;
  gViewPlayers = [NSMutableArray arrayWithObject:@{@"key":@"", @"title":@"主机当前视角"}];
  gViewPlayersTable = [[OvPlayersTable alloc] initWithFrame:playersScroll.contentView.bounds];
  NSTableColumn *playersColumn = [[NSTableColumn alloc] initWithIdentifier:@"player"];
  playersColumn.width = 380;
  [gViewPlayersTable addTableColumn:playersColumn];
  gViewPlayersTable.headerView = nil;
  gViewPlayersTable.rowHeight = 24;
  gViewPlayersTable.allowsMultipleSelection = NO;
  gViewPlayersTable.allowsEmptySelection = NO;
  gViewPlayersTable.columnAutoresizingStyle = NSTableViewLastColumnOnlyAutoresizingStyle;
  gViewPlayersTable.dataSource = gController;
  gViewPlayersTable.delegate = gController;
  gViewPlayersTable.toolTip = @"单击玩家，按该玩家的眼睛位置、朝向和视场角绘制骨骼世界。";
  playersScroll.documentView = gViewPlayersTable;
  [gConsole.contentView addSubview:playersScroll];
  gViewStatus = makeLabel(NSMakeRect(20, 386, 400, 24), @"");
  gViewStatus.lineBreakMode = NSLineBreakByTruncatingTail;
  [gConsole.contentView addSubview:gViewStatus];
  [gViewPlayersTable selectRowIndexes:[NSIndexSet indexSetWithIndex:0] byExtendingSelection:NO];
  ov_view_selection_changed();

  NSUserDefaults *defaults = NSUserDefaults.standardUserDefaults;
  [defaults registerDefaults:@{kOverlayOpacity:@100, kConsoleOpacity:@100, kDisplayDelay:@35}];
  [gConsole.contentView addSubview:makeLabel(NSMakeRect(20, 346, 130, 24), @"数据同步延迟")];
  gDisplayDelay = [[OvSlider alloc] initWithFrame:NSMakeRect(150, 346, 200, 24)];
  gDisplayDelay.minValue = 0;
  gDisplayDelay.maxValue = 250;
  gDisplayDelay.doubleValue = fmin(250, fmax(0, [defaults doubleForKey:kDisplayDelay]));
  gDisplayDelay.continuous = YES;
  gDisplayDelay.target = gController;
  gDisplayDelay.action = @selector(displayDelayChanged:);
  [gDisplayDelay setAccessibilityLabel:@"数据同步延迟"];
  [gConsole.contentView addSubview:gDisplayDelay];
  gDisplayDelayLabel = makeLabel(NSMakeRect(356, 346, 64, 24), @"");
  [gConsole.contentView addSubview:gDisplayDelayLabel];
  [gController displayDelayChanged:nil];
  NSTextField *syncHint = makeLabel(NSMakeRect(20, 316, 400, 24), @"0 ms 直接显示；增加延迟会缓冲并插值数据。");
  syncHint.font = [NSFont systemFontOfSize:11];
  [gConsole.contentView addSubview:syncHint];
  NSArray<NSString *> *opacityTitles = @[@"映射不透明度", @"面板不透明度"];
  NSArray<NSString *> *opacityKeys = @[kOverlayOpacity, kConsoleOpacity];
  for (NSUInteger i = 0; i < 2; ++i) {
    double y = 288 - i * 40;
    [gConsole.contentView addSubview:makeLabel(NSMakeRect(20, y, 130, 24), opacityTitles[i])];
    NSSlider *slider = [[OvSlider alloc] initWithFrame:NSMakeRect(150, y, 210, 24)];
    slider.minValue = i == 0 ? 0 : 40;
    slider.maxValue = 100;
    slider.doubleValue = fmin(100, fmax(slider.minValue, [defaults doubleForKey:opacityKeys[i]]));
    slider.continuous = YES;
    slider.target = gController;
    slider.action = @selector(appearanceChanged:);
    [slider setAccessibilityLabel:opacityTitles[i]];
    [gConsole.contentView addSubview:slider];
    gOpacityLabels[i] = makeLabel(NSMakeRect(370, y, 50, 24), @"");
    [gConsole.contentView addSubview:gOpacityLabels[i]];
    if (i == 0) gOverlayOpacity = slider;
    else gConsoleOpacity = slider;
  }

  NSArray<NSString *> *colorTitles = @[@"T 方框与骨骼", @"CT 方框与骨骼", @"正常血条", @"低血量血条", @"人物名称"];
  NSArray *savedColors = [defaults arrayForKey:kPlayerColors];
  for (NSUInteger i = 0; i < 5; ++i) {
    double x = i % 2 == 0 ? 20 : 240;
    double y = 207 - (i / 2) * 40;
    [gConsole.contentView addSubview:makeLabel(NSMakeRect(x, y + 4, 126, 24), colorTitles[i])];
    NSColorWell *well = [[OvColorWell alloc] initWithFrame:NSMakeRect(x + 128, y, 52, 28)];
    uint32_t rgb = gDefaultPlayerColors[i];
    if (i < savedColors.count && [savedColors[i] isKindOfClass:NSNumber.class] &&
        [savedColors[i] longLongValue] >= 0 && [savedColors[i] unsignedLongLongValue] <= 0xffffff) {
      rgb = [savedColors[i] unsignedIntValue];
    }
    well.color = [NSColor colorWithSRGBRed:((rgb >> 16) & 255) / 255.0
        green:((rgb >> 8) & 255) / 255.0 blue:(rgb & 255) / 255.0 alpha:1];
    well.continuous = YES;
    well.target = gController;
    well.action = @selector(appearanceChanged:);
    [well setAccessibilityLabel:[colorTitles[i] stringByAppendingString:@"颜色"]];
    [gConsole.contentView addSubview:well];
    gPlayerColors[i] = well;
  }
  NSButton *reset = [[OvButton alloc] initWithFrame:NSMakeRect(20, 72, 145, 30)];
  reset.title = @"恢复默认外观";
  reset.bezelStyle = NSBezelStyleRounded;
  reset.target = gController;
  reset.action = @selector(resetAppearance:);
  [gConsole.contentView addSubview:reset];
  gOverlayToggleButton = [[OvButton alloc] initWithFrame:NSMakeRect(240, 72, 180, 30)];
  gOverlayToggleButton.title = @"隐藏映射";
  gOverlayToggleButton.bezelStyle = NSBezelStyleRounded;
  gOverlayToggleButton.target = gController;
  gOverlayToggleButton.action = @selector(toggleOverlay:);
  [gConsole.contentView addSubview:gOverlayToggleButton];
  NSDictionary *savedHotkey = [defaults dictionaryForKey:kOverlayHotkeySetting];
  if ([savedHotkey[@"code"] isKindOfClass:NSNumber.class] &&
      [savedHotkey[@"modifiers"] isKindOfClass:NSNumber.class] &&
      [savedHotkey[@"code"] integerValue] >= 0 && [savedHotkey[@"code"] integerValue] <= 127 &&
      ([savedHotkey[@"modifiers"] unsignedIntValue] & ~(cmdKey | controlKey | optionKey | shiftKey)) == 0 &&
      [savedHotkey[@"title"] isKindOfClass:NSString.class] && [savedHotkey[@"title"] length] > 0 &&
      [savedHotkey[@"title"] length] <= 64 &&
      !([savedHotkey[@"code"] unsignedIntValue] == kVK_ANSI_P && [savedHotkey[@"modifiers"] unsignedIntValue] == cmdKey)) {
    gOverlayKeyCode = [savedHotkey[@"code"] unsignedIntValue];
    gOverlayKeyModifiers = [savedHotkey[@"modifiers"] unsignedIntValue];
    gOverlayKeyTitle = savedHotkey[@"title"];
  }
  [gConsole.contentView addSubview:makeLabel(NSMakeRect(20, 38, 140, 24), @"映射总开关键")];
  gOverlayHotkeyButton = [[OvHotkeyButton alloc] initWithFrame:NSMakeRect(160, 36, 260, 30)];
  gOverlayHotkeyButton.title = gOverlayKeyTitle;
  gOverlayHotkeyButton.bezelStyle = NSBezelStyleRounded;
  gOverlayHotkeyButton.target = gController;
  gOverlayHotkeyButton.action = @selector(recordOverlayHotkey:);
  [gOverlayHotkeyButton setAccessibilityLabel:@"绑定映射总开关键"];
  [gConsole.contentView addSubview:gOverlayHotkeyButton];
  gOverlayHotkeyStatus = makeLabel(NSMakeRect(20, 8, 400, 24), @"点击按键框绑定，按一下切换全部映射。");
  gOverlayHotkeyStatus.font = [NSFont systemFontOfSize:11];
  [gConsole.contentView addSubview:gOverlayHotkeyStatus];
  if (!gOverlayKeyMonitor) {
    gOverlayKeyMonitor = [NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskKeyDown | NSEventMaskKeyUp
        handler:^NSEvent *(NSEvent *event) { return ov_overlay_key_event(event); }];
  }
  ov_apply_appearance();
  [gConsole center];
}

static void ov_hide_color_panel(void) {
  if (!NSColorPanel.sharedColorPanelExists) return;
  for (NSUInteger i = 0; i < 5; ++i) [gPlayerColors[i] deactivate];
  [NSColorPanel.sharedColorPanel orderOut:nil];
}

static void ov_console_toggle(void) {
  if (!gMappingActive) return;
  ov_console_create();
  if (gConsole.isVisible) {
    ov_cancel_hotkey_recording();
    ov_hide_color_panel();
    [gConsole orderOut:nil];
    gOverlay.level = NSScreenSaverWindowLevel;
    [gReturnApplication activateWithOptions:0];
    gReturnApplication = nil;
  } else {
    NSRunningApplication *front = NSWorkspace.sharedWorkspace.frontmostApplication;
    if (front.processIdentifier != getpid()) gReturnApplication = front;
    gOverlay.level = NSFloatingWindowLevel;
    [gConsole makeKeyAndOrderFront:nil];
    [NSApp activateIgnoringOtherApps:YES];
    [gConsole makeKeyWindow];
    [gConsole makeFirstResponder:gViewPlayersTable];
  }
}

int ov_controls_enable(int enabled) {
  if (gMappingActive == (enabled != 0)) return 0;
  if (enabled) {
    ov_console_create();
    if (!gConsoleHandler) {
      EventTypeSpec types[] = {{kEventClassKeyboard, kEventHotKeyPressed}, {kEventClassKeyboard, kEventHotKeyReleased}};
      OSStatus installed = InstallApplicationEventHandler(ov_console_hotkey, 2, types, NULL, &gConsoleHandler);
      if (installed != noErr) return (int)installed;
    }
    EventHotKeyID key = {'CVRW', 1};
    OSStatus status = RegisterEventHotKey(kVK_ANSI_P, cmdKey, key, GetApplicationEventTarget(), 0, &gConsoleHotKey);
    gMappingActive = YES;
    ov_register_overlay_hotkey(gOverlayKeyCode, gOverlayKeyModifiers);
    return (int)status;
  }
  ov_hide_color_panel();
  [gConsole orderOut:nil];
  gOverlay.level = NSScreenSaverWindowLevel;
  gReturnApplication = nil;
  gMappingActive = NO;
  ov_cancel_hotkey_recording();
  if (gOverlayHotkey) { UnregisterEventHotKey(gOverlayHotkey); gOverlayHotkey = NULL; }
  gOverlayHotkeyDown = NO;
  gConsoleToggle = NO;
  if (gConsoleHotKey) { UnregisterEventHotKey(gConsoleHotKey); gConsoleHotKey = NULL; }
  return 0;
}

unsigned int ov_controls_flags(void) {
  unsigned int flags = 0;
  for (unsigned int i = 0; i < 5; ++i)
    if (!gConsole || gDisplayChecks[i].state == NSControlStateValueOn) flags |= 1u << i;
  return flags;
}

int ov_controls_overlay_hidden(void) { return gOverlayHidden ? 1 : 0; }

int ov_controls_delay_ms(void) { return gDisplayDelay ? (int)gDisplayDelay.integerValue : 35; }

static uint32_t ov_rgb_channel(CGFloat value) {
  return (uint32_t)lround(fmin(1, fmax(0, value)) * 255);
}

uint32_t ov_controls_color(int index) {
  if (index < 0 || index >= 5) return 0xffffff;
  if (!gPlayerColors[index]) return gDefaultPlayerColors[index];
  NSColor *color = [gPlayerColors[index].color colorUsingColorSpace:NSColorSpace.sRGBColorSpace];
  if (!color) return gDefaultPlayerColors[index];
  return (ov_rgb_channel(color.redComponent) << 16) |
         (ov_rgb_channel(color.greenComponent) << 8) |
          ov_rgb_channel(color.blueComponent);
}

void ov_controls_set_players(const char *lines) {
  ov_console_create();
  NSString *selected = [NSString stringWithUTF8String:gViewKey];
  NSInteger selectedRow = gViewPlayersTable.selectedRow;
  NSString *oldTitle = selectedRow >= 0 && selectedRow < (NSInteger)gViewPlayers.count ? gViewPlayers[selectedRow][@"title"] : @"所选玩家";
  NSString *waitingSuffix = @" · 等待玩家返回";
  if ([oldTitle hasSuffix:waitingSuffix]) oldTitle = [oldTitle substringToIndex:oldTitle.length - waitingSuffix.length];
  gUpdatingViewPlayers = YES;
  [gViewPlayers removeAllObjects];
  [gViewPlayers addObject:@{@"key":@"", @"title":@"主机当前视角"}];
  NSString *all = [NSString stringWithUTF8String:lines ?: ""];
  NSInteger match = selected.length ? -1 : 0;
  for (NSString *line in [all componentsSeparatedByString:@"\n"]) {
    NSRange separator = [line rangeOfString:@"\t"];
    if (separator.location == NSNotFound) continue;
    NSString *key = [line substringToIndex:separator.location];
    NSString *label = [line substringFromIndex:separator.location + 1];
    [gViewPlayers addObject:@{@"key":key, @"title":label}];
    if ([key isEqualToString:selected]) match = gViewPlayers.count - 1;
  }
  if (match < 0) {
    [gViewPlayers addObject:@{@"key":selected, @"title":[oldTitle stringByAppendingString:waitingSuffix]}];
    match = gViewPlayers.count - 1;
  }
  [gViewPlayersTable reloadData];
  [gViewPlayersTable selectRowIndexes:[NSIndexSet indexSetWithIndex:match] byExtendingSelection:NO];
  [gViewPlayersTable scrollRowToVisible:match];
  gUpdatingViewPlayers = NO;
  ov_view_selection_changed();
}

void ov_controls_set_view_key(const char *key) {
  ov_console_create();
  NSString *wanted = [NSString stringWithUTF8String:key ?: ""];
  for (NSUInteger i = 0; i < gViewPlayers.count; ++i) {
    if ([gViewPlayers[i][@"key"] isEqual:wanted]) {
      [gViewPlayersTable selectRowIndexes:[NSIndexSet indexSetWithIndex:i] byExtendingSelection:NO];
      [gViewPlayersTable scrollRowToVisible:i];
      ov_view_selection_changed();
      return;
    }
  }
}

static void ov_view_selection_changed(void) {
  NSInteger row = gViewPlayersTable.selectedRow;
  if (row < 0 || row >= (NSInteger)gViewPlayers.count) return;
  NSString *key = gViewPlayers[row][@"key"];
  const char *utf8 = key.UTF8String;
  strncpy(gViewKey, utf8 ?: "", sizeof(gViewKey) - 1);
  gViewKey[sizeof(gViewKey) - 1] = '\0';
  gViewStatus.stringValue = [@"当前视角  " stringByAppendingString:gViewPlayers[row][@"title"]];
  gViewStatus.toolTip = gViewStatus.stringValue;
}

const char *ov_controls_view_key(void) {
  return gViewKey;
}

// ---------------------------------------------------------------- lifecycle

void ov_init(void) {
  @autoreleasepool {
    [NSApplication sharedApplication];
    [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
    [NSApp finishLaunching];

    if (!NSApp.mainMenu) {
      NSMenu *menubar = [[NSMenu alloc] init];
      NSMenuItem *appItem = [[NSMenuItem alloc] init];
      [menubar addItem:appItem];

      NSMenu *appMenu = [[NSMenu alloc] init];
      [appMenu addItemWithTitle:@"Quit cs2 remote viewer"
                         action:@selector(terminate:)
                  keyEquivalent:@"q"];
      appItem.submenu = appMenu;
      NSApp.mainMenu = menubar;
    }

    if (!gController) {
      gController = [[OvController alloc] init];
    }

    if (!gStatusItem) {
      gStatusItem = [[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];
      gStatusItem.button.title = @"CS2";

      NSMenu *menu = [[NSMenu alloc] init];
      NSMenuItem *show = [[NSMenuItem alloc] initWithTitle:@"Show launcher"
                                                    action:@selector(showLauncher:)
                                             keyEquivalent:@""];
      show.target = gController;
      [menu addItem:show];

      NSMenuItem *controls = [[NSMenuItem alloc] initWithTitle:@"显示控制台 (⌘P)"
          action:@selector(toggleConsole:) keyEquivalent:@""];
      controls.target = gController;
      [menu addItem:controls];

      NSMenuItem *quit = [[NSMenuItem alloc] initWithTitle:@"Quit"
                                                    action:@selector(quitClicked:)
                                             keyEquivalent:@"q"];
      quit.target = gController;
      [menu addItem:quit];

      gStatusItem.menu = menu;
    }
  }
}

void ov_pump(void) {
  @autoreleasepool {
    CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0, true);
    for (;;) {
      NSEvent *event = [NSApp nextEventMatchingMask:NSEventMaskAny
                                          untilDate:[NSDate distantPast]
                                             inMode:NSDefaultRunLoopMode
                                            dequeue:YES];
      if (!event) break;
      [NSApp sendEvent:event];
    }
    if (gConsoleToggle) { gConsoleToggle = NO; ov_console_toggle(); }
    [NSApp updateWindows];
    [CATransaction flush];
  }
}

// ---------------------------------------------------------------- launcher

void ov_launcher_create(const char *server, const char *room, const char *key) {
  @autoreleasepool {
    NSRect frame = NSMakeRect(0, 0, 580, 560);
    gLauncher = [[NSWindow alloc] initWithContentRect:frame
                                            styleMask:NSWindowStyleMaskTitled |
                                                      NSWindowStyleMaskClosable |
                                                      NSWindowStyleMaskMiniaturizable
                                              backing:NSBackingStoreBuffered
                                                defer:NO];
    gLauncher.title = @"cs2 remote viewer";
    [gLauncher center];

    NSView *content = gLauncher.contentView;

    [content addSubview:makeLabel(NSMakeRect(16, 512, 90, 20), @"Server")];
    gServerField = makeField(NSMakeRect(110, 508, 440, 24), [NSString stringWithUTF8String:server ?: ""]);
    [content addSubview:gServerField];

    [content addSubview:makeLabel(NSMakeRect(16, 476, 90, 20), @"Room code")];
    gRoomField = makeField(NSMakeRect(110, 472, 200, 24), [NSString stringWithUTF8String:room ?: ""]);
    [content addSubview:gRoomField];

    [content addSubview:makeLabel(NSMakeRect(16, 440, 90, 20), @"Viewer key")];
    gKeyField = makeField(NSMakeRect(110, 436, 440, 24), [NSString stringWithUTF8String:key ?: ""]);
    [content addSubview:gKeyField];

    NSButton *connect = [[OvButton alloc] initWithFrame:NSMakeRect(110, 396, 120, 30)];
    connect.title = @"Connect";
    connect.bezelStyle = NSBezelStyleRounded;
    connect.target = gController;
    connect.action = @selector(connectClicked:);
    [content addSubview:connect];

    gStatusLabel = makeLabel(NSMakeRect(244, 380, 316, 56), @"Paste the room code and viewer key from the host.");
    gStatusLabel.lineBreakMode = NSLineBreakByWordWrapping;
    gStatusLabel.maximumNumberOfLines = 3;
    [content addSubview:gStatusLabel];

    [content addSubview:makeLabel(NSMakeRect(16, 360, 120, 20), @"视角玩家")];
    gPlayerPopup = [[OvPopUpButton alloc] initWithFrame:NSMakeRect(16, 328, 440, 28) pullsDown:NO];
    [content addSubview:gPlayerPopup];

    NSButton *prevPlayer = [[OvButton alloc] initWithFrame:NSMakeRect(464, 328, 44, 28)];
    prevPlayer.title = @"<";
    prevPlayer.bezelStyle = NSBezelStyleRounded;
    prevPlayer.target = gController;
    prevPlayer.action = @selector(prevPlayer:);
    [content addSubview:prevPlayer];

    NSButton *nextPlayer = [[OvButton alloc] initWithFrame:NSMakeRect(512, 328, 44, 28)];
    nextPlayer.title = @">";
    nextPlayer.bezelStyle = NSBezelStyleRounded;
    nextPlayer.target = gController;
    nextPlayer.action = @selector(nextPlayer:);
    [content addSubview:nextPlayer];

    gWindowLabel = makeLabel(NSMakeRect(16, 296, 120, 20), @"Overlay window");
    [content addSubview:gWindowLabel];
    gWindowPopup = [[OvPopUpButton alloc] initWithFrame:NSMakeRect(16, 264, 450, 28) pullsDown:NO];
    [content addSubview:gWindowPopup];

    gWindowRefresh = [[OvButton alloc] initWithFrame:NSMakeRect(474, 264, 82, 28)];
    gWindowRefresh.title = @"Refresh";
    gWindowRefresh.bezelStyle = NSBezelStyleRounded;
    gWindowRefresh.target = gController;
    gWindowRefresh.action = @selector(refreshClicked:);
    [content addSubview:gWindowRefresh];
    gAutomaticTargetLabel = makeLabel(NSMakeRect(16, 258, 540, 58),
        @"自动定位 GeForce NOW，可直接开始映射。\n切换桌面、全屏或恢复窗口时自动重新跟随。");
    gAutomaticTargetLabel.maximumNumberOfLines = 2;
    [content addSubview:gAutomaticTargetLabel];

    NSButton *start = [[OvButton alloc] initWithFrame:NSMakeRect(16, 210, 160, 34)];
    start.title = @"Start overlay";
    start.bezelStyle = NSBezelStyleRounded;
    start.keyEquivalent = @"\r";
    start.target = gController;
    start.action = @selector(startClicked:);
    [content addSubview:start];

    gFollowGeForce = [[OvButton alloc] initWithFrame:NSMakeRect(16, 170, 300, 26)];
    [gFollowGeForce setButtonType:NSButtonTypeSwitch];
    gFollowGeForce.title = @"Follow GeForce NOW automatically";
    gFollowGeForce.state = NSControlStateValueOn;
    gFollowGeForce.target = gController;
    gFollowGeForce.action = @selector(followGeForceChanged:);
    [content addSubview:gFollowGeForce];
    [gController followGeForceChanged:gFollowGeForce];
    [content addSubview:makeLabel(NSMakeRect(16, 132, 100, 24), @"Stream ratio")];
    gStreamAspect = [[OvPopUpButton alloc] initWithFrame:NSMakeRect(116, 130, 220, 28) pullsDown:NO];
    [gStreamAspect addItemsWithTitles:@[@"Auto (host camera)", @"16:9", @"16:10", @"21:9", @"4:3", @"Fill window"]];
    [content addSubview:gStreamAspect];
    [content addSubview:makeLabel(NSMakeRect(16, 94, 120, 24), @"Top inset (pt)")];
    gTopInset = makeField(NSMakeRect(140, 92, 70, 24), @"0");
    [content addSubview:gTopInset];

    [gLauncher makeKeyAndOrderFront:nil];
    [NSApp activateIgnoringOtherApps:YES];
    [gLauncher makeFirstResponder:gRoomField];

    // Activation is sometimes ignored while the process is still starting.
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.25 * NSEC_PER_SEC)),
                   dispatch_get_main_queue(), ^{
      if (!gLauncher.isVisible) return;
      [NSApp activateIgnoringOtherApps:YES];
      [gLauncher makeKeyAndOrderFront:nil];
    });
  }
}

void ov_launcher_show(void) {
  if (!gLauncher) return;
  [gLauncher makeKeyAndOrderFront:nil];
  [NSApp activateIgnoringOtherApps:YES];
}

void ov_launcher_hide(void) {
  if (!gLauncher) return;
  [gLauncher orderOut:nil];
}

int ov_launcher_visible(void) { return gLauncher && gLauncher.isVisible ? 1 : 0; }

int ov_launcher_take_connect(void) { BOOL v = gConnectFlag; gConnectFlag = NO; return v ? 1 : 0; }
int ov_launcher_take_start(void) { BOOL v = gStartFlag; gStartFlag = NO; return v ? 1 : 0; }
int ov_launcher_take_refresh(void) { BOOL v = gRefreshFlag; gRefreshFlag = NO; return v ? 1 : 0; }
int ov_take_quit(void) { BOOL v = gQuitFlag; gQuitFlag = NO; return v ? 1 : 0; }
int ov_take_show_launcher(void) { BOOL v = gShowLauncherFlag; gShowLauncherFlag = NO; return v ? 1 : 0; }

void ov_launcher_get(int field, char *buffer, int length) {
  if (!buffer || length <= 0) return;
  NSTextField *target = nil;
  switch (field) {
    case 0: target = gServerField; break;
    case 1: target = gRoomField; break;
    case 2: target = gKeyField; break;
    default: break;
  }
  NSString *value = target ? target.stringValue : @"";
  const char *utf8 = value.UTF8String;
  strncpy(buffer, utf8 ? utf8 : "", (size_t)length - 1);
  buffer[length - 1] = '\0';
}

void ov_launcher_set_status(const char *text) {
  if (!gStatusLabel) return;
  gStatusLabel.stringValue = [NSString stringWithUTF8String:text ?: ""];
}

void ov_launcher_set_players(const char *newline_separated) {
  if (!gPlayerPopup) return;

  NSString *all = [NSString stringWithUTF8String:newline_separated ?: ""];
  NSArray *items = all.length ? [all componentsSeparatedByString:@"\n"] : @[];

  if (items.count == 0) {
    items = @[ @"waiting for the host..." ];
  }

  NSInteger selected = gPlayerPopup.indexOfSelectedItem;
  [gPlayerPopup removeAllItems];
  [gPlayerPopup addItemsWithTitles:items];

  if (selected >= 0 && selected < (NSInteger)items.count) {
    [gPlayerPopup selectItemAtIndex:selected];
  } else if (items.count > 0) {
    [gPlayerPopup selectItemAtIndex:0];
  }
}

int ov_launcher_player_index(void) {
  if (!gPlayerPopup) return -1;
  return (int)gPlayerPopup.indexOfSelectedItem;
}

void ov_launcher_set_windows(const char *newline_separated) {
  if (!gWindowPopup) return;

  NSString *all = [NSString stringWithUTF8String:newline_separated ?: ""];
  NSArray *items = all.length ? [all componentsSeparatedByString:@"\n"] : @[];

  if (items.count == 0) {
    items = @[ @"no windows found - press Refresh" ];
  }

  NSInteger selected = gWindowPopup.indexOfSelectedItem;
  [gWindowPopup removeAllItems];
  [gWindowPopup addItemsWithTitles:items];

  if (selected >= 0 && selected < (NSInteger)items.count) {
    [gWindowPopup selectItemAtIndex:selected];
  } else if (items.count > 0) {
    [gWindowPopup selectItemAtIndex:0];
  }
}

// ov_ui_test opens the player popup, records whether a menu window appeared,
// then dismisses it with Escape. Used to verify the dropdown works.
void ov_ui_test(void) {
  if (!gPlayerPopup) return;

  fprintf(stderr, "[ui] before items=%ld enabled=%d key_window=%d active=%d\n",
          (long)gPlayerPopup.numberOfItems, gPlayerPopup.isEnabled ? 1 : 0,
          gLauncher.isKeyWindow ? 1 : 0, NSApp.isActive ? 1 : 0);

  __block BOOL sawMenu = NO;

  dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.6 * NSEC_PER_SEC)),
                 dispatch_get_main_queue(), ^{
    for (NSWindow *w in [NSApp windows]) {
      if ([NSStringFromClass([w class]) containsString:@"Menu"]) sawMenu = YES;
    }
    fprintf(stderr, "[ui] during: items=%ld menu_window=%d\n",
            (long)gPlayerPopup.numberOfItems, sawMenu ? 1 : 0);

    NSEvent *escape = [NSEvent keyEventWithType:NSEventTypeKeyDown
                                      location:NSZeroPoint
                                 modifierFlags:0
                                     timestamp:0
                                  windowNumber:0
                                       context:nil
                                    characters:@"\e"
                   charactersIgnoringModifiers:@"\e"
                                     isARepeat:NO
                                       keyCode:53];
    [NSApp postEvent:escape atStart:YES];
  });

  [gPlayerPopup performClick:nil];

  fprintf(stderr, "[ui] after items=%ld\n", (long)gPlayerPopup.numberOfItems);
}

void ov_launcher_pick(int index) {
  if (!gPlayerPopup) return;
  if (index >= 0 && index < (int)gPlayerPopup.numberOfItems) {
    [gPlayerPopup selectItemAtIndex:index];
  }
}

void ov_launcher_force_start(void) { gStartFlag = YES; }

int ov_launcher_window_index(void) {
  if (!gWindowPopup) return -1;
  return (int)gWindowPopup.indexOfSelectedItem;
}

int ov_follow_geforce(void) { return gFollowGeForce.state == NSControlStateValueOn; }
int ov_stream_aspect(void) { return (int)gStreamAspect.indexOfSelectedItem; }
double ov_top_inset(void) { return fmax(0, gTopInset.doubleValue); }

static BOOL ov_is_geforce(NSDictionary *info) {
  pid_t pid = [info[(id)kCGWindowOwnerPID] intValue];
  NSRunningApplication *app = [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
  NSString *bundle = app.bundleIdentifier.lowercaseString;
  NSString *owner = [info[(id)kCGWindowOwnerName] lowercaseString];
  return [bundle hasPrefix:@"com.nvidia.gfn"] || [owner containsString:@"geforce now"] ||
         [owner containsString:@"geforcenow"];
}

int ov_geforce_frontmost(void) {
  NSRunningApplication *front = NSWorkspace.sharedWorkspace.frontmostApplication;
  if (!front) return 0;
  if (front.processIdentifier == getpid()) return 1;
  return ov_is_geforce(@{(id)kCGWindowOwnerPID: @(front.processIdentifier),
                        (id)kCGWindowOwnerName: front.localizedName ?: @""});
}

uint32_t ov_geforce_window_id(void) {
  @autoreleasepool {
    CFArrayRef windows = CGWindowListCopyWindowInfo(
        kCGWindowListOptionAll | kCGWindowListExcludeDesktopElements, kCGNullWindowID);
    if (!windows) return 0;
    uint32_t best = 0;
    double area = 0;
    BOOL bestOnscreen = NO;
    for (NSDictionary *info in (__bridge NSArray *)windows) {
      // Streaming clients can replace the normal window with a raised window
      // when entering full screen. Its layer does not have to remain zero.
      if (!ov_is_geforce(info)) continue;
      CGRect bounds;
      NSDictionary *dictionary = info[(id)kCGWindowBounds];
      if (!dictionary || !CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)dictionary, &bounds)) continue;
      double size = bounds.size.width * bounds.size.height;
      BOOL onscreen = [info[(id)kCGWindowIsOnscreen] boolValue];
      if (bounds.size.width >= 320 && bounds.size.height >= 180 &&
          (!best || (onscreen && !bestOnscreen) || (onscreen == bestOnscreen && size > area))) {
        area = size;
        bestOnscreen = onscreen;
        best = [info[(id)kCGWindowNumber] unsignedIntValue];
      }
    }
    CFRelease(windows);
    return best;
  }
}

// ---------------------------------------------------------------- window list

const char *ov_windows_list(void) {
  @autoreleasepool {
    gWindowTitles[0] = '\0';

    if (!gWindowNumbers) gWindowNumbers = [NSMutableArray array];
    if (!gWindowBounds) gWindowBounds = [NSMutableArray array];
    [gWindowNumbers removeAllObjects];
    [gWindowBounds removeAllObjects];

    CFArrayRef list = CGWindowListCopyWindowInfo(
        kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements,
        kCGNullWindowID);
    if (!list) return gWindowTitles;

    pid_t self = getpid();
    size_t used = 0;

    for (NSDictionary *info in (__bridge NSArray *)list) {
      NSNumber *layer = info[(id)kCGWindowLayer];
      if (layer.integerValue != 0) continue;

      NSNumber *pid = info[(id)kCGWindowOwnerPID];
      if (pid.intValue == self) continue;

      NSString *name = info[(id)kCGWindowName];
      if (!name.length) name = info[(id)kCGWindowOwnerName];
      if (!name.length) continue;

      CGRect bounds = CGRectZero;
      NSDictionary *boundsDict = info[(id)kCGWindowBounds];
      if (!boundsDict || !CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)boundsDict, &bounds)) continue;
      if (bounds.size.width < 40 || bounds.size.height < 40) continue;

      NSNumber *number = info[(id)kCGWindowNumber];
      if (!number) continue;

      [gWindowNumbers addObject:number];
      [gWindowBounds addObject:[NSValue valueWithRect:NSMakeRect(bounds.origin.x, bounds.origin.y, bounds.size.width, bounds.size.height)]];

      const char *utf8 = name.UTF8String;
      if (!utf8) continue;
      size_t len = strlen(utf8);
      if (used + len + 2 >= sizeof(gWindowTitles)) break;
      memcpy(gWindowTitles + used, utf8, len);
      used += len;
      gWindowTitles[used++] = '\n';
      gWindowTitles[used] = '\0';
    }

    CFRelease(list);

    // Drop the trailing newline.
    if (used > 0) gWindowTitles[used - 1] = '\0';

    return gWindowTitles;
  }
}

void ov_screen_size(double *w, double *h) {
  NSRect frame = [NSScreen mainScreen].frame;
  if (w) *w = frame.size.width;
  if (h) *h = frame.size.height;
}

uint32_t ov_launcher_window_id(int index) {
  if (!gWindowNumbers || index < 0 || index >= (int)gWindowNumbers.count) return 0;
  return gWindowNumbers[index].unsignedIntValue;
}

int ov_window_count(void) {
  return gWindowBounds ? (int)gWindowBounds.count : 0;
}

// ov_window_bounds_by_id queries one window by CGWindowID, so the overlay can
// follow moves and resizes every frame without re-enumerating everything.
// The returned rect is in top-left screen points, which is what
// ov_overlay_begin expects.
int ov_window_bounds_by_id(uint32_t wid, double *x, double *y, double *w, double *h) {
  if (wid == 0) return 0;

  CFArrayRef list = CGWindowListCopyWindowInfo(kCGWindowListOptionIncludingWindow, wid);
  if (!list) return 0;

  int found = 0;
  for (NSDictionary *info in (__bridge NSArray *)list) {
    NSNumber *number = info[(id)kCGWindowNumber];
    if (number.unsignedIntValue != wid || ![info[(id)kCGWindowIsOnscreen] boolValue]) continue;

    NSDictionary *boundsDict = info[(id)kCGWindowBounds];
    CGRect r = CGRectZero;
    if (!boundsDict || !CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)boundsDict, &r)) continue;

    if (x) *x = r.origin.x;
    if (y) *y = r.origin.y;
    if (w) *w = r.size.width;
    if (h) *h = r.size.height;
    found = 1;
    break;
  }

  CFRelease(list);
  return found;
}

// ---------------------------------------------------------------- overlay

void ov_overlay_create(void) {
  if (gOverlay) return;

  NSRect frame = NSMakeRect(0, 0, 400, 300);
  gOverlay = [[NSPanel alloc] initWithContentRect:frame
                                          styleMask:NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel
                                            backing:NSBackingStoreBuffered
                                              defer:NO];
  gOverlay.hidesOnDeactivate = NO;
  gOverlay.releasedWhenClosed = NO;
  gOverlay.animationBehavior = NSWindowAnimationBehaviorNone;
  gOverlay.opaque = NO;
  gOverlay.backgroundColor = [NSColor clearColor];
  gOverlay.hasShadow = NO;
  gOverlay.ignoresMouseEvents = YES;
  gOverlay.alphaValue = gOverlayOpacity ? gOverlayOpacity.doubleValue / 100 : 1;
  gOverlay.level = NSScreenSaverWindowLevel;
  gOverlay.collectionBehavior = ov_floating_collection_behavior() |
                                NSWindowCollectionBehaviorStationary |
                                NSWindowCollectionBehaviorIgnoresCycle;

  gOverlayView = [[NSView alloc] initWithFrame:frame];
  gOverlayView.wantsLayer = YES;
  gOverlayView.layer.opaque = NO;
  gOverlayView.layer.backgroundColor = CGColorGetConstantColor(kCGColorClear);

  gOverlay.contentView = gOverlayView;
  if (!gOverlayHidden) {
    [gOverlay orderFrontRegardless];
    gOverlayVisible = YES;
  }
}

void ov_overlay_show(void) {
  if (!gOverlay || gOverlayHidden) return;
  [gOverlay orderFrontRegardless];
  gOverlayVisible = YES;
}

void ov_overlay_hide(void) {
  if (!gOverlay) return;
  [gOverlay orderOut:nil];
  gOverlayVisible = NO;
}

static void ov_ensure_context(double w, double h, double scale) {
  if (gCtx && w == gW && h == gH && scale == gScale) return;

  if (gCtx) {
    CGContextRelease(gCtx);
    gCtx = NULL;
  }

  size_t pw = (size_t)(w * scale);
  size_t ph = (size_t)(h * scale);
  if (pw < 1) pw = 1;
  if (ph < 1) ph = 1;

  CGColorSpaceRef space = CGColorSpaceCreateDeviceRGB();
  gCtx = CGBitmapContextCreate(NULL, pw, ph, 8, pw * 4, space,
                               kCGImageAlphaPremultipliedFirst | kCGBitmapByteOrder32Little);
  CGColorSpaceRelease(space);

  gW = w;
  gH = h;
  gScale = scale;
}

void ov_overlay_begin(double x, double y, double w, double h) {
  if (!gOverlay) return;

  double screenHeight = NSMaxY(NSScreen.screens.firstObject.frame);
  NSRect frame = NSMakeRect(x, screenHeight - (y + h), w, h);
  [gOverlay setFrame:frame display:NO];

  double scale = gOverlay.backingScaleFactor;
  ov_ensure_context(w, h, scale);

  if (!gCtx) return;

  CGContextClearRect(gCtx, CGRectMake(0, 0, w * scale, h * scale));
  CGContextSaveGState(gCtx);
  CGContextScaleCTM(gCtx, scale, scale);
  CGContextSetShouldAntialias(gCtx, true);
}

void ov_overlay_fill(double x, double y, double w, double h,
                     double r, double g, double b, double a) {
  if (!gCtx) return;
  CGContextSetRGBFillColor(gCtx, r, g, b, a);
  CGContextFillRect(gCtx, CGRectMake(x, gH - y - h, w, h));
}

void ov_overlay_line(double x1, double y1, double x2, double y2,
                     double r, double g, double b, double a) {
  if (!gCtx) return;
  CGContextSetRGBStrokeColor(gCtx, r, g, b, a);
  CGContextSetLineWidth(gCtx, 1.0);
  CGContextBeginPath(gCtx);
  CGContextMoveToPoint(gCtx, x1, gH - y1);
  CGContextAddLineToPoint(gCtx, x2, gH - y2);
  CGContextStrokePath(gCtx);
}

void ov_overlay_text(double x, double y, const char *utf8,
                     double r, double g, double b, double a) {
  if (!gCtx || !utf8 || !*utf8) return;

  @autoreleasepool {
    NSString *string = [NSString stringWithUTF8String:utf8];
    if (!string) return;

    NSFont *font = [NSFont fontWithName:kOvFontName size:12];
    if (!font) font = [NSFont monospacedSystemFontOfSize:12 weight:NSFontWeightRegular];

    NSDictionary *attributes = @{
      NSFontAttributeName : font,
      NSForegroundColorAttributeName : [NSColor colorWithSRGBRed:r green:g blue:b alpha:a],
    };

    NSAttributedString *attributed = [[NSAttributedString alloc] initWithString:string
                                                                     attributes:attributes];
    CTLineRef line = CTLineCreateWithAttributedString((__bridge CFAttributedStringRef)attributed);
    if (!line) return;

    CGFloat ascent = 0;
    CTLineGetTypographicBounds(line, &ascent, NULL, NULL);

    CGContextSetTextMatrix(gCtx, CGAffineTransformIdentity);
    CGContextSetTextPosition(gCtx, x, gH - y - ascent);
    CTLineDraw(line, gCtx);
    CFRelease(line);
  }
}

// ov_selftest_dump draws a known shape into a fresh context and writes it out,
// to prove the Core Graphics path independently of the overlay window.
int ov_selftest_dump(const char *path) {
  CGColorSpaceRef space = CGColorSpaceCreateDeviceRGB();
  CGContextRef ctx = CGBitmapContextCreate(NULL, 200, 100, 8, 800, space,
                                           kCGImageAlphaPremultipliedFirst | kCGBitmapByteOrder32Little);
  CGColorSpaceRelease(space);
  if (!ctx) return 0;

  CGContextSetRGBFillColor(ctx, 1, 0, 0, 1);
  CGContextFillRect(ctx, CGRectMake(10, 10, 100, 50));

  CGImageRef image = CGBitmapContextCreateImage(ctx);
  CGContextRelease(ctx);
  if (!image) return 0;

  CFURLRef url = CFURLCreateFromFileSystemRepresentation(NULL, (const UInt8 *)path, (CFIndex)strlen(path), false);
  int ok = 0;
  if (url) {
    CGImageDestinationRef dest = CGImageDestinationCreateWithURL(url, CFSTR("public.png"), 1, NULL);
    if (dest) {
      CGImageDestinationAddImage(dest, image, NULL);
      ok = CGImageDestinationFinalize(dest) ? 1 : 0;
      CFRelease(dest);
    }
    CFRelease(url);
  }
  CGImageRelease(image);
  return ok;
}

// ov_follow_test verifies that window bounds are re-read as the window moves.
int ov_follow_test(void) {
  NSWindow *probe = [[NSWindow alloc] initWithContentRect:NSMakeRect(120, 140, 400, 300)
                                                styleMask:NSWindowStyleMaskTitled
                                                  backing:NSBackingStoreBuffered
                                                    defer:NO];
  probe.title = @"follow test";
  [probe orderFrontRegardless];
  [NSRunLoop.currentRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:0.05]];

  uint32_t wid = (uint32_t)probe.windowNumber;

  double x1 = 0, y1 = 0, w1 = 0, h1 = 0;
  int ok1 = ov_window_bounds_by_id(wid, &x1, &y1, &w1, &h1);

  [probe setFrame:NSMakeRect(320, 220, 640, 480) display:NO];
  [NSRunLoop.currentRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:0.05]];

  double x2 = 0, y2 = 0, w2 = 0, h2 = 0;
  int ok2 = ov_window_bounds_by_id(wid, &x2, &y2, &w2, &h2);

  [probe orderOut:nil];

  fprintf(stderr, "[follow] before %.0fx%.0f at %.0f,%.0f | after %.0fx%.0f at %.0f,%.0f\n",
          w1, h1, x1, y1, w2, h2, x2, y2);

  return (ok1 && ok2 && (w2 != w1 || h2 != h1)) ? 1 : 0;
}

// ov_overlay_dump writes the current overlay bitmap to a PNG, for tests.
int ov_overlay_dump(const char *path) {
  if (!gCtx || !path) return 0;

  CGImageRef image = CGBitmapContextCreateImage(gCtx);
  if (!image) return 0;

  CFURLRef url = CFURLCreateFromFileSystemRepresentation(
      NULL, (const UInt8 *)path, (CFIndex)strlen(path), false);
  if (!url) {
    CGImageRelease(image);
    return 0;
  }

  CGImageDestinationRef dest = CGImageDestinationCreateWithURL(url, CFSTR("public.png"), 1, NULL);
  int ok = 0;
  if (dest) {
    CGImageDestinationAddImage(dest, image, NULL);
    ok = CGImageDestinationFinalize(dest) ? 1 : 0;
    CFRelease(dest);
  }

  CFRelease(url);
  CGImageRelease(image);
  return ok;
}

void ov_overlay_present(void) {
  if (!gCtx || !gOverlayView) return;

  CGContextRestoreGState(gCtx);
  CGContextFlush(gCtx);

  CGImageRef image = CGBitmapContextCreateImage(gCtx);
  if (!image) return;

  gOverlayView.layer.contents = (__bridge id)image;
  gOverlayView.layer.contentsScale = gScale;
  [CATransaction flush];
  CGImageRelease(image);
}
