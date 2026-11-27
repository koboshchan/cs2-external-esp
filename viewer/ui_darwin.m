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

#include <string.h>
#include <unistd.h>

// ---------------------------------------------------------------- state

static NSWindow *gLauncher = nil;
static NSTextField *gServerField = nil;
static NSTextField *gRoomField = nil;
static NSTextField *gKeyField = nil;
static NSTextField *gStatusLabel = nil;
static NSPopUpButton *gPlayerPopup = nil;
static NSPopUpButton *gWindowPopup = nil;

static BOOL gConnectFlag = NO;
static BOOL gStartFlag = NO;
static BOOL gRefreshFlag = NO;
static BOOL gQuitFlag = NO;

static NSWindow *gOverlay = nil;
static NSView *gOverlayView = nil;
static CGContextRef gCtx = NULL;
static double gW = 0, gH = 0, gScale = 1;
static BOOL gOverlayVisible = NO;

static NSStatusItem *gStatusItem = nil;
static id gController = nil;

// Window list for the picker.
static NSMutableArray<NSNumber *> *gWindowNumbers = nil;
static NSMutableArray<NSValue *> *gWindowBounds = nil;
static char gWindowTitles[16384];

// ---------------------------------------------------------------- helpers

static NSString *const kOvFontName = @"Menlo";

@interface OvController : NSObject
- (void)connectClicked:(id)sender;
- (void)startClicked:(id)sender;
- (void)refreshClicked:(id)sender;
- (void)showLauncher:(id)sender;
- (void)quitClicked:(id)sender;
@end

@implementation OvController

- (void)connectClicked:(id)sender { gConnectFlag = YES; }
- (void)startClicked:(id)sender { gStartFlag = YES; }
- (void)refreshClicked:(id)sender { gRefreshFlag = YES; }

- (void)showLauncher:(id)sender {
  if (!gLauncher) return;
  [gLauncher makeKeyAndOrderFront:nil];
  [NSApp activateIgnoringOtherApps:YES];
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

// ---------------------------------------------------------------- lifecycle

void ov_init(void) {
  @autoreleasepool {
    [NSApplication sharedApplication];
    [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];

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
    for (;;) {
      NSEvent *event = [NSApp nextEventMatchingMask:NSEventMaskAny
                                          untilDate:[NSDate distantPast]
                                             inMode:NSDefaultRunLoopMode
                                            dequeue:YES];
      if (!event) break;
      [NSApp sendEvent:event];
    }
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

    NSButton *connect = [[NSButton alloc] initWithFrame:NSMakeRect(110, 396, 120, 30)];
    connect.title = @"Connect";
    connect.bezelStyle = NSBezelStyleRounded;
    connect.target = gController;
    connect.action = @selector(connectClicked:);
    [content addSubview:connect];

    gStatusLabel = makeLabel(NSMakeRect(244, 392, 310, 40), @"Paste the room code and viewer key from the host.");
    gStatusLabel.lineBreakMode = NSLineBreakByWordWrapping;
    [content addSubview:gStatusLabel];

    [content addSubview:makeLabel(NSMakeRect(16, 360, 120, 20), @"You are")];
    gPlayerPopup = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(16, 328, 540, 28) pullsDown:NO];
    [content addSubview:gPlayerPopup];

    [content addSubview:makeLabel(NSMakeRect(16, 296, 120, 20), @"Overlay window")];
    gWindowPopup = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(16, 264, 450, 28) pullsDown:NO];
    [content addSubview:gWindowPopup];

    NSButton *refresh = [[NSButton alloc] initWithFrame:NSMakeRect(474, 264, 82, 28)];
    refresh.title = @"Refresh";
    refresh.bezelStyle = NSBezelStyleRounded;
    refresh.target = gController;
    refresh.action = @selector(refreshClicked:);
    [content addSubview:refresh];

    NSButton *start = [[NSButton alloc] initWithFrame:NSMakeRect(16, 210, 160, 34)];
    start.title = @"Start overlay";
    start.bezelStyle = NSBezelStyleRounded;
    start.keyEquivalent = @"\r";
    start.target = gController;
    start.action = @selector(startClicked:);
    [content addSubview:start];

    [gLauncher makeKeyAndOrderFront:nil];
    [NSApp activateIgnoringOtherApps:YES];
    [gLauncher makeFirstResponder:gRoomField];
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

  NSInteger selected = gWindowPopup.indexOfSelectedItem;
  [gWindowPopup removeAllItems];
  [gWindowPopup addItemsWithTitles:items];

  if (selected >= 0 && selected < (NSInteger)items.count) {
    [gWindowPopup selectItemAtIndex:selected];
  } else if (items.count > 0) {
    [gWindowPopup selectItemAtIndex:0];
  }
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

// ov_window_bounds returns the window frame in screen points with a top-left
// origin, matching the overlay coordinates.
int ov_window_count(void) {
  return gWindowBounds ? (int)gWindowBounds.count : 0;
}

int ov_window_bounds(int index, double *x, double *y, double *w, double *h) {
  if (!gWindowBounds || index < 0 || index >= (int)gWindowBounds.count) return 0;

  NSRect r = gWindowBounds[index].rectValue;
  double screenHeight = [NSScreen mainScreen].frame.size.height;

  if (x) *x = r.origin.x;
  if (y) *y = screenHeight - (r.origin.y + r.size.height);
  if (w) *w = r.size.width;
  if (h) *h = r.size.height;

  return 1;
}

// ---------------------------------------------------------------- overlay

void ov_overlay_create(void) {
  if (gOverlay) return;

  NSRect frame = NSMakeRect(0, 0, 400, 300);
  gOverlay = [[NSWindow alloc] initWithContentRect:frame
                                          styleMask:NSWindowStyleMaskBorderless
                                            backing:NSBackingStoreBuffered
                                              defer:NO];
  gOverlay.opaque = NO;
  gOverlay.backgroundColor = [NSColor clearColor];
  gOverlay.hasShadow = NO;
  gOverlay.ignoresMouseEvents = YES;
  gOverlay.level = NSScreenSaverWindowLevel;
  gOverlay.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces |
                                NSWindowCollectionBehaviorStationary |
                                NSWindowCollectionBehaviorFullScreenAuxiliary |
                                NSWindowCollectionBehaviorIgnoresCycle;

  gOverlayView = [[NSView alloc] initWithFrame:frame];
  gOverlayView.wantsLayer = YES;
  gOverlayView.layer.opaque = NO;
  gOverlayView.layer.backgroundColor = CGColorGetConstantColor(kCGColorClear);

  gOverlay.contentView = gOverlayView;
  [gOverlay orderFrontRegardless];
  gOverlayVisible = YES;
}

void ov_overlay_show(void) {
  if (!gOverlay) return;
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

  double screenHeight = [NSScreen mainScreen].frame.size.height;
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
  CGImageRelease(image);
}
