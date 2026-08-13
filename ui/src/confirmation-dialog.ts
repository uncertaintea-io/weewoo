export interface ConfirmationOptions {
  title: string;
  message: string;
  confirmLabel: string;
  dangerous?: boolean;
}

export interface PromptOptions {
  title: string;
  message: string;
  confirmLabel: string;
}

interface DialogOptions extends ConfirmationOptions {
  input?: boolean;
  cancelable?: boolean;
}

function openDialog(options: DialogOptions): Promise<boolean | string> {
  return new Promise((resolve) => {
    const previouslyFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
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

    const input = options.input === true ? document.createElement('input') : null;
    if (input !== null) {
      input.className = 'confirmation-input';
      input.type = 'text';
      input.setAttribute('aria-label', options.message);
    }

    const actions = document.createElement('div');
    actions.className = 'confirmation-actions';

    const cancel = document.createElement('button');
    if (options.cancelable !== false) {
      cancel.className = 'secondary-button';
      cancel.type = 'button';
      cancel.textContent = 'Cancel';
      actions.append(cancel);
    }

    const confirm = document.createElement('button');
    confirm.className = options.dangerous === true ? 'danger-button' : 'primary-button';
    confirm.type = 'button';
    confirm.textContent = options.confirmLabel;

    const finish = (result: boolean | string): void => {
      document.removeEventListener('keydown', onKeyDown);
      backdrop.remove();
      previouslyFocused?.focus();
      resolve(result);
    };
    const onKeyDown = (event: KeyboardEvent): void => {
      if (event.key === 'Escape' && options.cancelable !== false) finish(false);
      if (event.key === 'Enter' && input !== null) finish(input.value);
    };

    cancel.addEventListener('click', () => { finish(false); });
    confirm.addEventListener('click', () => { finish(input?.value ?? true); });
    backdrop.addEventListener('click', (event) => {
      if (event.target === backdrop && options.cancelable !== false) finish(false);
    });
    document.addEventListener('keydown', onKeyDown);

    actions.append(confirm);
    dialog.append(title, message);
    if (input !== null) dialog.append(input);
    dialog.append(actions);
    backdrop.append(dialog);
    document.body.append(backdrop);
    (input ?? confirm).focus();
  });
}

export async function confirmAction(options: ConfirmationOptions): Promise<boolean> {
  return await openDialog(options) === true;
}

export async function promptAction(options: PromptOptions): Promise<string | undefined> {
  const result = await openDialog({ ...options, input: true });
  return typeof result === 'string' ? result : undefined;
}

export async function showMessage(title: string, message: string): Promise<void> {
  await openDialog({ title, message, confirmLabel: 'OK', cancelable: false });
}
