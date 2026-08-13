export interface ConfirmationOptions {
  title: string;
  message: string;
  confirmLabel: string;
  dangerous?: boolean;
}

interface DialogOptions extends ConfirmationOptions {
  cancelable?: boolean;
}

function openDialog(options: DialogOptions): Promise<boolean> {
  return new Promise((resolve) => {
    const previouslyFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const app = document.querySelector<HTMLElement>('#app');
    const appWasInert = app?.inert ?? false;
    const backdrop = document.createElement('div');
    backdrop.className = 'confirmation-backdrop';

    const dialog = document.createElement('section');
    dialog.className = 'confirmation-dialog';
    dialog.setAttribute('role', 'alertdialog');
    dialog.setAttribute('aria-modal', 'true');
    dialog.setAttribute('aria-labelledby', 'confirmation-title');
    dialog.setAttribute('aria-describedby', 'confirmation-message');

    const title = document.createElement('h2');
    title.id = 'confirmation-title';
    title.textContent = options.title;

    const message = document.createElement('p');
    message.id = 'confirmation-message';
    message.textContent = options.message;

    const actions = document.createElement('div');
    actions.className = 'confirmation-actions';

    const cancel = document.createElement('button');
    cancel.className = 'secondary-button';
    cancel.type = 'button';
    cancel.textContent = 'Cancel';

    const confirm = document.createElement('button');
    confirm.className = options.dangerous === true ? 'danger-button' : 'primary-button';
    confirm.type = 'button';
    confirm.textContent = options.confirmLabel;

    let finished = false;
    const finish = (result: boolean): void => {
      if (finished) return;
      finished = true;
      document.removeEventListener('keydown', onKeyDown);
      backdrop.remove();
      if (app !== null) app.inert = appWasInert;
      previouslyFocused?.focus();
      resolve(result);
    };
    const onKeyDown = (event: KeyboardEvent): void => {
      if (event.key === 'Escape' && options.cancelable !== false) {
        event.preventDefault();
        finish(false);
        return;
      }
      if (event.key !== 'Tab') return;

      const focusable = Array.from(dialog.querySelectorAll<HTMLElement>('button:not(:disabled)'));
      if (focusable.length === 0) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];

      if (!dialog.contains(document.activeElement)) {
        event.preventDefault();
        (event.shiftKey ? last : first).focus();
      } else if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };

    cancel.addEventListener('click', () => { finish(false); });
    confirm.addEventListener('click', () => { finish(true); });
    backdrop.addEventListener('click', (event) => {
      if (event.target === backdrop && options.cancelable !== false) finish(false);
    });
    document.addEventListener('keydown', onKeyDown);

    if (options.cancelable !== false) actions.append(cancel);
    actions.append(confirm);
    dialog.append(title, message, actions);
    backdrop.append(dialog);
    if (app !== null) app.inert = true;
    document.body.append(backdrop);
    confirm.focus();
  });
}

export async function confirmAction(options: ConfirmationOptions): Promise<boolean> {
  return await openDialog(options);
}

export async function showMessage(title: string, message: string): Promise<void> {
  await openDialog({ title, message, confirmLabel: 'OK', cancelable: false });
}
