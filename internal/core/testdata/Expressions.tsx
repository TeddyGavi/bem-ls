import React, { useState } from 'react';
import clsx from 'clsx';
import styles from './Expressions.module.scss';

type Props<T> = { items: T[]; size: 'sm' | 'lg'; active?: boolean };

const isSmall = (n: number) => n < 10 && n > 0;
const pattern = /<div className="fake">/g;

export function List<T extends { id: string }>({ items, size, active }: Props<T>) {
  const [open, setOpen] = useState<boolean>(false);
  // <span className="commented-out"></span>
  return (
    <>
      <ul
        className={clsx('list', `list--${size}`, {
          'list--open': open,
          'list--active': active,
        })}
        data-count={items.length > 3 ? 'many' : 'few'}
        {...rest}
      >
        {items.map((item) => (
          <li key={item.id} className={size === 'sm' ? 'list__item list__item--small' : 'list__item'}>
            <span className={styles.label}>{item.id}</span>
            <button disabled className="list__button" onClick={() => setOpen(!open)} />
          </li>
        ))}
      </ul>
      {open && <p className={'list__empty ' + (items.length ? '' : 'list__empty--visible')}>Nothing's here &lt; yet</p>}
    </>
  );
}

export const Badge = () => <span className="badge"><i className="badge__icon" /></span>;
